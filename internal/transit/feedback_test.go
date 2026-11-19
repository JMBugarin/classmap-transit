package transit

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCleanMessage(t *testing.T) {
	for in, want := range map[string]string{
		"  Door is behind\nthe vending   machines \t": "Door is behind the vending machines",
		"a\x00b\x07c":                         "abc",
		"":                                    "",
		strings.Repeat("é", FeedbackMaxRunes): strings.Repeat("é", FeedbackMaxRunes),
	} {
		if got, e := CleanMessage(in); e != nil || got != want {
			t.Errorf("%q: got %q, %v", in, got, e)
		}
	}
	for _, bad := range []string{strings.Repeat("a", FeedbackMaxRunes+1), "\xff\xfe"} {
		if _, e := CleanMessage(bad); e == nil {
			t.Errorf("accepted %q", bad[:min(len(bad), 12)])
		}
	}
}

func TestFeedbackLimiter(t *testing.T) {
	var l feedbackLimiter
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	for i := 0; i < feedbackPerMinute; i++ {
		if !l.allow(now.Add(time.Duration(i) * time.Second)) {
			t.Fatalf("blocked request %d", i)
		}
	}
	if l.allow(now.Add(30 * time.Second)) {
		t.Fatal("allowed more than the per-minute limit")
	}
	if !l.allow(now.Add(61 * time.Second)) {
		t.Fatal("did not reset after a minute")
	}
}

func TestRoomFeedbackKeepsOnlyWhatWasSent(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	now := time.Date(2003, 3, 12, 20, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	if _, e := s.DB.Exec(ctx, "DELETE FROM room_feedback WHERE received_at < '2004-01-01'"); e != nil {
		t.Fatal(e)
	}
	a := &API{Store: s, Origin: "https://jmbugarin.github.io"}
	post := func(body string) int {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("POST", "/v1/room-feedback", strings.NewReader(body)))
		return w.Code
	}
	for _, ok := range []string{
		`{"place":"baskin-engineering-152","kind":"easy"}`,
		`{"place":"mchenry-library-1340","kind":"unclear","message":"Pin is on the wrong floor"}`,
		`{"place":"mchenry-library-1340","kind":"tip","message":"  Take the elevator\nby the café "}`,
		`{"place":"crown-classroom","kind":"missing","message":"Crown 207"}`,
	} {
		if code := post(ok); code != 204 {
			t.Fatalf("%s: %d", ok, code)
		}
	}
	for _, bad := range []string{
		`{"place":"Baskin 152","kind":"easy"}`,
		`{"place":"baskin-engineering-152","kind":"rating"}`,
		`{"place":"baskin-engineering-152","kind":"tip"}`,
		`{"place":"baskin-engineering-152","kind":"tip","message":"   "}`,
		`{"place":"baskin-engineering-152","kind":"easy","install":"x"}`,
		`{"place":"baskin-engineering-152","kind":"tip","message":"` + strings.Repeat("a", 281) + `"}`,
		`{"place":`,
	} {
		if code := post(bad); code != 400 {
			t.Errorf("%s: got %d want 400", bad[:min(len(bad), 60)], code)
		}
	}
	rows, e := s.FeedbackReport(ctx, false)
	if e != nil {
		t.Fatal(e)
	}
	var mine []Feedback
	for _, f := range rows {
		if f.Received.Before(time.Date(2004, 1, 1, 0, 0, 0, 0, time.UTC)) {
			mine = append(mine, f)
		}
	}
	if len(mine) != 4 || mine[0].Kind != "easy" || mine[0].Message != nil || *mine[2].Message != "Take the elevator by the café" {
		t.Fatalf("report %+v", mine)
	}
	if n, e := s.ReviewFeedback(ctx, []int64{mine[0].ID, mine[1].ID}); e != nil || n != 2 {
		t.Fatal(n, e)
	}
	if n, _ := s.ReviewFeedback(ctx, []int64{mine[0].ID}); n != 0 {
		t.Fatal("reviewed the same row twice")
	}
	now = now.Add(91 * 24 * time.Hour)
	if n, e := s.PruneFeedback(ctx); e != nil || n < 2 {
		t.Fatal("prune", n, e)
	}
	var left int
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM room_feedback WHERE id = ANY($1)", []int64{mine[2].ID, mine[3].ID}).Scan(&left); e != nil || left != 2 {
		t.Fatal("prune removed unreviewed feedback", left, e)
	}
	var cols []string
	cr, _ := s.DB.Query(ctx, "SELECT column_name FROM information_schema.columns WHERE table_name='room_feedback' ORDER BY column_name")
	for cr.Next() {
		var c string
		_ = cr.Scan(&c)
		cols = append(cols, c)
	}
	if strings.Join(cols, ",") != "id,kind,message,place,received_at,reviewed_at" {
		t.Fatalf("room_feedback stores more than it should: %v", cols)
	}

	pre := func(origin string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("OPTIONS", "/v1/room-feedback", nil)
		r.Header.Set("Origin", origin)
		a.ServeHTTP(w, r)
		return w
	}
	if w := pre("https://jmbugarin.github.io"); w.Code != 204 || w.Header().Get("Access-Control-Allow-Methods") != "POST" || w.Header().Get("Access-Control-Allow-Origin") != "https://jmbugarin.github.io" {
		t.Fatalf("preflight %d %v", w.Code, w.Header())
	}
	if w := pre("https://example.com"); w.Code != 403 {
		t.Fatalf("preflight from another origin: %d", w.Code)
	}
}

func TestRoomFeedbackCaps(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	now := time.Date(2002, 6, 5, 19, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	if _, e := s.DB.Exec(ctx, "DELETE FROM room_feedback WHERE received_at < '2003-01-01'"); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _, _ = s.DB.Exec(ctx, "DELETE FROM room_feedback WHERE received_at < '2003-01-01'") })
	a := &API{Store: s}
	post := func() int {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("POST", "/v1/room-feedback", strings.NewReader(`{"place":"thimann-labs-101","kind":"easy"}`)))
		return w.Code
	}
	for i := 0; i < feedbackPerMinute; i++ {
		if code := post(); code != 204 {
			t.Fatalf("post %d: %d", i, code)
		}
	}
	if code := post(); code != 429 {
		t.Fatalf("per-minute cap: %d", code)
	}
	if _, e := s.DB.Exec(ctx, "INSERT INTO room_feedback(received_at,place,kind) SELECT $1, 'thimann-labs-101', 'easy' FROM generate_series(1,$2)", now, feedbackPerDay); e != nil {
		t.Fatal(e)
	}
	now = now.Add(2 * time.Minute)
	if code := post(); code != 429 {
		t.Fatalf("daily cap: %d", code)
	}
	now = now.Add(24 * time.Hour)
	if code := post(); code != 204 {
		t.Fatalf("next day: %d", code)
	}
}
