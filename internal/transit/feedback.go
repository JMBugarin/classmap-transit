package transit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

var placePattern = regexp.MustCompile(`^[a-z0-9-]{1,80}$`)

const (
	FeedbackMaxRunes   = 280
	feedbackPerMinute  = 20
	feedbackPerDay     = 500
	feedbackKeepAfter  = 90 * 24 * time.Hour
	feedbackBodyLimit  = 2048
	feedbackReportSize = 200
)

var ErrBusy = errors.New("feedback limit reached")

var feedbackKinds = map[string]bool{"easy": true, "unclear": true, "tip": true, "missing": true}

type Feedback struct {
	ID       int64      `json:"id"`
	Received time.Time  `json:"received_at"`
	Place    string     `json:"place"`
	Kind     string     `json:"kind"`
	Message  *string    `json:"message,omitempty"`
	Reviewed *time.Time `json:"reviewed_at,omitempty"`
}

// CleanMessage trims a note, drops control characters and enforces the length limit.
func CleanMessage(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", fmt.Errorf("%w: message is not text", ErrInvalid)
	}
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > FeedbackMaxRunes {
		return "", fmt.Errorf("%w: message longer than %d characters", ErrInvalid, FeedbackMaxRunes)
	}
	return s, nil
}

// feedbackLimiter caps how fast anonymous feedback can arrive; the API never sees client addresses.
type feedbackLimiter struct {
	mu     sync.Mutex
	window time.Time
	count  int
}

func (l *feedbackLimiter) allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.window) >= time.Minute {
		l.window, l.count = now, 0
	}
	if l.count >= feedbackPerMinute {
		return false
	}
	l.count++
	return true
}

func (s *Store) RecordFeedback(ctx context.Context, place, kind, message string) error {
	if !placePattern.MatchString(place) {
		return fmt.Errorf("%w: invalid place", ErrInvalid)
	}
	if !feedbackKinds[kind] {
		return fmt.Errorf("%w: kind must be easy, unclear, tip or missing", ErrInvalid)
	}
	msg, e := CleanMessage(message)
	if e != nil {
		return e
	}
	if msg == "" && (kind == "tip" || kind == "missing") {
		return fmt.Errorf("%w: a %s needs a message", ErrInvalid, kind)
	}
	now := s.Now()
	day := now.In(pacific)
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, pacific)
	var today int
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM room_feedback WHERE received_at >= $1", start).Scan(&today); e != nil {
		return e
	}
	if today >= feedbackPerDay {
		return ErrBusy
	}
	var m *string
	if msg != "" {
		m = &msg
	}
	_, e = s.DB.Exec(ctx, "INSERT INTO room_feedback(received_at,place,kind,message) VALUES($1,$2,$3,$4)", now, place, kind, m)
	return e
}

// FeedbackReport lists feedback oldest first; unreviewed only unless all is set.
func (s *Store) FeedbackReport(ctx context.Context, all bool) ([]Feedback, error) {
	rows, e := s.DB.Query(ctx, `SELECT id,received_at,place,kind,message,reviewed_at FROM room_feedback
		WHERE $1 OR reviewed_at IS NULL ORDER BY received_at, id LIMIT $2`, all, feedbackReportSize)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Feedback{}
	for rows.Next() {
		var f Feedback
		if e = rows.Scan(&f.ID, &f.Received, &f.Place, &f.Kind, &f.Message, &f.Reviewed); e != nil {
			return nil, e
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) ReviewFeedback(ctx context.Context, ids []int64) (int64, error) {
	tag, e := s.DB.Exec(ctx, "UPDATE room_feedback SET reviewed_at=$2 WHERE id=ANY($1) AND reviewed_at IS NULL", ids, s.Now())
	return tag.RowsAffected(), e
}

// PruneFeedback deletes feedback reviewed more than 90 days before now.
func (s *Store) PruneFeedback(ctx context.Context) (int64, error) {
	tag, e := s.DB.Exec(ctx, "DELETE FROM room_feedback WHERE reviewed_at < $1", s.Now().Add(-feedbackKeepAfter))
	return tag.RowsAffected(), e
}

func (a *API) serveFeedback(w http.ResponseWriter, r *http.Request, fail func(int, string)) {
	switch r.Method {
	case "POST":
		var body struct {
			Place   string `json:"place"`
			Kind    string `json:"kind"`
			Message string `json:"message"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, feedbackBodyLimit))
		dec.DisallowUnknownFields()
		if e := dec.Decode(&body); e != nil {
			fail(400, "invalid body")
			return
		}
		if !a.feedback.allow(a.Store.Now()) {
			fail(429, "too much feedback right now, try again in a minute")
			return
		}
		if e := a.Store.RecordFeedback(r.Context(), body.Place, body.Kind, body.Message); e != nil {
			switch {
			case errors.Is(e, ErrInvalid):
				fail(400, e.Error())
			case errors.Is(e, ErrBusy):
				fail(429, e.Error())
			default:
				fail(503, "database unavailable")
			}
			return
		}
		w.WriteHeader(204)
	case "OPTIONS":
		if a.Origin == "" || r.Header.Get("Origin") != a.Origin {
			fail(403, "origin not allowed")
			return
		}
		w.Header().Set("Access-Control-Allow-Methods", "POST")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Max-Age", "86400")
		w.WriteHeader(204)
	default:
		fail(405, "method not allowed")
	}
}
