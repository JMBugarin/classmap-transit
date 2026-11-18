package transit

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func install(t *testing.T) string {
	t.Helper()
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func TestWeekStartUsesPacificMondays(t *testing.T) {
	for _, c := range []struct{ at, want string }{
		{"2026-11-23T07:30:00Z", "2026-11-16"},
		{"2026-11-23T08:10:00Z", "2026-11-23"},
		{"2026-11-18T19:00:00Z", "2026-11-16"},
		{"2026-11-02T07:59:00Z", "2026-10-26"},
		{"2026-11-02T08:00:00Z", "2026-11-02"},
		{"2027-03-15T06:30:00Z", "2027-03-08"},
		{"2027-03-15T07:00:00Z", "2027-03-15"},
	} {
		at, _ := time.Parse(time.RFC3339, c.at)
		if got := WeekStart(at).Format("2006-01-02"); got != c.want {
			t.Errorf("%s: got %s want %s", c.at, got, c.want)
		}
	}
}

func TestReadCohort(t *testing.T) {
	got, e := ReadCohort(strings.NewReader("# consented 2026-11\nA1B2C3D4\n\n  0f0f0f0f  # session 2\n"))
	if e != nil || len(got) != 2 || got[0] != "a1b2c3d4" || got[1] != "0f0f0f0f" {
		t.Fatal(got, e)
	}
	if _, e = ReadCohort(strings.NewReader("alice\n")); e == nil {
		t.Fatal("accepted a name as a participant code")
	}
}

func TestActivityCountsOncePerWeekAndHonoursWithdrawal(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	now, _ := time.Parse(time.RFC3339, "2003-03-12T20:00:00Z")
	s.Now = func() time.Time { return now }
	if _, e := s.DB.Exec(ctx, "DELETE FROM activity WHERE week < '2004-01-01'"); e != nil {
		t.Fatal(e)
	}
	a := &API{Store: s}
	post := func(body string) int {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("POST", "/v1/activity", strings.NewReader(body)))
		return w.Code
	}
	one, two, dev := install(t), install(t), install(t)
	thisWeek, lastWeek := "2003-03-10", "2003-03-03"
	for _, c := range []struct {
		id, week string
		times    int
	}{{one, lastWeek, 3}, {two, lastWeek, 1}, {dev, lastWeek, 1}, {one, "2003-02-24", 1}, {one, thisWeek, 1}} {
		for i := 0; i < c.times; i++ {
			if code := post(fmt.Sprintf(`{"install":%q,"week":%q}`, c.id, c.week)); code != 204 {
				t.Fatalf("%s %s: %d", c.id, c.week, code)
			}
		}
	}
	for _, bad := range []string{
		fmt.Sprintf(`{"install":%q,"week":"2003-03-11"}`, one),
		fmt.Sprintf(`{"install":%q,"week":"2003-03-17"}`, one),
		fmt.Sprintf(`{"install":%q,"week":"2003-01-27"}`, one),
		`{"install":"not-a-uuid","week":"2003-03-10"}`,
		fmt.Sprintf(`{"install":%q,"week":"2003-03-10","stop":"1594"}`, one),
		`{"install":`,
	} {
		if code := post(bad); code != 400 {
			t.Errorf("%s: got %d want 400", bad, code)
		}
	}
	if e := s.ExcludeActivity(ctx, dev); e != nil {
		t.Fatal(e)
	}
	weeks, e := s.ActivityReport(ctx, []string{one[:8], "deadbeef"})
	if e != nil {
		t.Fatal(e)
	}
	if len(weeks) != 2 || weeks[0].Week != "2003-02-24" || weeks[1].Week != lastWeek {
		t.Fatalf("weeks %+v (the current week must be left out)", weeks)
	}
	if weeks[1].Installs != 2 || *weeks[1].Participants != 1 || weeks[0].Installs != 1 || *weeks[0].Participants != 1 {
		t.Fatalf("counts %+v %+v", weeks[0], *weeks[1].Participants)
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("DELETE", "/v1/activity?install="+one, nil))
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
	var left int
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM activity WHERE install=$1", one).Scan(&left); e != nil || left != 0 {
		t.Fatal("withdrawal kept rows", left, e)
	}
	if weeks, _ = s.ActivityReport(ctx, nil); len(weeks) != 1 || weeks[0].Installs != 1 || weeks[0].Participants != nil {
		t.Fatalf("after withdrawal %+v", weeks)
	}
	var cols []string
	rows, _ := s.DB.Query(ctx, "SELECT column_name FROM information_schema.columns WHERE table_name='activity' ORDER BY column_name")
	for rows.Next() {
		var c string
		_ = rows.Scan(&c)
		cols = append(cols, c)
	}
	if strings.Join(cols, ",") != "install,week" {
		t.Fatalf("activity stores more than install and week: %v", cols)
	}
}

func TestActivityPreflightOnlyForTheAllowedOrigin(t *testing.T) {
	a := &API{Origin: "https://jmbugarin.github.io"}
	for _, c := range []struct {
		origin string
		code   int
	}{{"https://jmbugarin.github.io", 204}, {"https://example.com", 403}, {"", 403}} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("OPTIONS", "/v1/activity", nil)
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		a.ServeHTTP(w, r)
		if w.Code != c.code {
			t.Fatalf("%q: got %d want %d", c.origin, w.Code, c.code)
		}
		allowed := w.Header().Get("Access-Control-Allow-Origin")
		if c.code == 204 && (allowed != c.origin || !strings.Contains(w.Header().Get("Access-Control-Allow-Methods"), "DELETE")) {
			t.Fatalf("preflight headers %v", w.Header())
		}
		if c.code != 204 && allowed != "" {
			t.Fatalf("allowed origin %q for %q", allowed, c.origin)
		}
	}
}
