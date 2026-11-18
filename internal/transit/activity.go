package transit

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata"
)

var ErrInvalid = errors.New("invalid input")

var installPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

const activityWeeksBack = 5

var pacific = mustZone("America/Los_Angeles")

func mustZone(name string) *time.Location {
	l, e := time.LoadLocation(name)
	if e != nil {
		panic(e)
	}
	return l
}

func WeekStart(t time.Time) time.Time {
	d := t.In(pacific)
	day := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
	return day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
}

func (s *Store) RecordActivity(ctx context.Context, install, week string) error {
	if !installPattern.MatchString(install) {
		return fmt.Errorf("%w: invalid install", ErrInvalid)
	}
	w, e := time.Parse("2006-01-02", week)
	if e != nil || w.Weekday() != time.Monday {
		return fmt.Errorf("%w: week must be a Monday", ErrInvalid)
	}
	current := WeekStart(s.Now())
	if w.After(current) || w.Before(current.AddDate(0, 0, -7*activityWeeksBack)) {
		return fmt.Errorf("%w: week out of range", ErrInvalid)
	}
	_, e = s.DB.Exec(ctx, "INSERT INTO activity(install,week) VALUES($1,$2) ON CONFLICT DO NOTHING", install, w)
	return e
}

func (s *Store) DeleteActivity(ctx context.Context, install string) (int64, error) {
	if !installPattern.MatchString(install) {
		return 0, fmt.Errorf("%w: invalid install", ErrInvalid)
	}
	tag, e := s.DB.Exec(ctx, "DELETE FROM activity WHERE install=$1", install)
	return tag.RowsAffected(), e
}

// ExcludeActivity takes a full install id or its 8-character participant code.
func (s *Store) ExcludeActivity(ctx context.Context, install string) error {
	code := strings.ToLower(install)
	if installPattern.MatchString(code) {
		code = code[:8]
	}
	if len(code) != 8 || strings.Trim(code, "0123456789abcdef") != "" {
		return fmt.Errorf("%w: invalid install or participant code", ErrInvalid)
	}
	_, e := s.DB.Exec(ctx, "INSERT INTO activity_excluded_codes(code) VALUES($1) ON CONFLICT DO NOTHING", code)
	return e
}

type WeekCount struct {
	Week         string `json:"week"`
	Installs     int    `json:"active_installs"`
	Participants *int   `json:"cohort_participants,omitempty"`
}

// Cohort entries are participant codes: the first 8 hex digits of an install id.
func ReadCohort(r io.Reader) ([]string, error) {
	var codes []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(strings.SplitN(sc.Text(), "#", 2)[0])
		if line == "" {
			continue
		}
		code := strings.ToLower(line)
		if len(code) != 8 || strings.Trim(code, "0123456789abcdef") != "" {
			return nil, fmt.Errorf("invalid participant code %q", line)
		}
		codes = append(codes, code)
	}
	return codes, sc.Err()
}

func (s *Store) ActivityReport(ctx context.Context, cohort []string) ([]WeekCount, error) {
	rows, e := s.DB.Query(ctx, `SELECT to_char(a.week,'YYYY-MM-DD'), count(DISTINCT a.install), count(DISTINCT left(a.install::text,8)) FILTER (WHERE left(a.install::text,8) = ANY($1))
		FROM activity a WHERE NOT EXISTS (SELECT 1 FROM activity_excluded x WHERE x.install=a.install) AND NOT EXISTS (SELECT 1 FROM activity_excluded_codes x WHERE x.code=left(a.install::text,8)) AND a.week < $2
		GROUP BY a.week ORDER BY a.week`, cohort, WeekStart(s.Now()))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []WeekCount{}
	for rows.Next() {
		var c WeekCount
		var p int
		if e = rows.Scan(&c.Week, &c.Installs, &p); e != nil {
			return nil, e
		}
		if cohort != nil {
			c.Participants = &p
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (a *API) serveActivity(w http.ResponseWriter, r *http.Request, fail func(int, string)) {
	switch r.Method {
	case "POST":
		var body struct {
			Install string `json:"install"`
			Week    string `json:"week"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		dec.DisallowUnknownFields()
		if e := dec.Decode(&body); e != nil {
			fail(400, "invalid body")
			return
		}
		if e := a.Store.RecordActivity(r.Context(), body.Install, body.Week); e != nil {
			if errors.Is(e, ErrInvalid) {
				fail(400, e.Error())
				return
			}
			fail(503, "database unavailable")
			return
		}
		w.WriteHeader(204)
	case "DELETE":
		if _, e := a.Store.DeleteActivity(r.Context(), r.URL.Query().Get("install")); e != nil {
			if errors.Is(e, ErrInvalid) {
				fail(400, e.Error())
				return
			}
			fail(503, "database unavailable")
			return
		}
		w.WriteHeader(204)
	case "OPTIONS":
		if a.Origin == "" || r.Header.Get("Origin") != a.Origin {
			fail(403, "origin not allowed")
			return
		}
		w.Header().Set("Access-Control-Allow-Methods", "POST, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Max-Age", "86400")
		w.WriteHeader(204)
	default:
		fail(405, "method not allowed")
	}
}
