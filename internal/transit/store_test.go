package transit

import (
	"context"
	"encoding/json"
	"fmt"
	gtfs "github.com/MobilityData/gtfs-realtime-bindings/golang/gtfs"
	"google.golang.org/protobuf/proto"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func capture(ts uint64, stop string) []byte {
	m := &gtfs.FeedMessage{Header: &gtfs.FeedHeader{GtfsRealtimeVersion: proto.String("2.0"), Timestamp: proto.Uint64(ts)}}
	if stop != "" {
		m.Entity = []*gtfs.FeedEntity{{Id: proto.String("one"), TripUpdate: &gtfs.TripUpdate{Trip: &gtfs.TripDescriptor{TripId: proto.String("trip")}, Timestamp: proto.Uint64(ts), StopTimeUpdate: []*gtfs.TripUpdate_StopTimeUpdate{{StopId: proto.String(stop), Departure: &gtfs.TripUpdate_StopTimeEvent{Time: proto.Int64(int64(ts) + 300)}}}}}}
	}
	b, _ := proto.Marshal(m)
	return b
}
func setup(t *testing.T) (*Store, string) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("requires real PostgreSQL TEST_DATABASE_URL")
	}
	s, e := Open(context.Background(), url)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.DB.Close)
	if e = s.Migrate(context.Background()); e != nil {
		t.Fatal(e)
	}
	return s, fmt.Sprintf("test-%s-%d", t.Name(), time.Now().UnixNano())
}
func process(t *testing.T, s *Store, ns string, b []byte) int64 {
	t.Helper()
	ctx := context.Background()
	id, e := s.Accept(ctx, ns, "trips", "", b, s.Now())
	if e != nil {
		t.Fatal(e)
	}
	j, e := s.ClaimNamespace(ctx, ns)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Process(ctx, j); e != nil {
		t.Fatal(e)
	}
	return id
}
func TestPublicationOrderingAndReplacement(t *testing.T) {
	s, ns := setup(t)
	ctx := context.Background()
	now := s.Now().Unix()
	first := process(t, s, ns, capture(uint64(now), "a"))
	process(t, s, ns, capture(uint64(now-1), "b"))
	process(t, s, ns, capture(uint64(now), "b"))
	var gen int64
	if e := s.DB.QueryRow(ctx, "SELECT generation FROM feed_state WHERE ns=$1", ns).Scan(&gen); e != nil || gen != first {
		t.Fatalf("regressed %d %v", gen, e)
	}
	last := process(t, s, ns, capture(uint64(now+1), ""))
	var n int
	if e := s.DB.QueryRow(ctx, "SELECT count(*) FROM observations WHERE generation=$1", last).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	var conflicts int
	_ = s.DB.QueryRow(ctx, "SELECT count(*) FROM outcomes o JOIN captures c ON c.id=o.capture WHERE c.ns=$1 AND outcome='conflict'", ns).Scan(&conflicts)
	if conflicts != 1 {
		t.Fatal(conflicts)
	}
}
func TestDuplicateAcceptanceAndConstraints(t *testing.T) {
	s, ns := setup(t)
	ctx := context.Background()
	b := capture(uint64(s.Now().Unix()), "a")
	id, e := s.Accept(ctx, ns, "trips", "", b, s.Now())
	if e != nil {
		t.Fatal(e)
	}
	id2, e := s.Accept(ctx, ns, "trips", "", b, s.Now())
	if e != nil || id != id2 {
		t.Fatal(id, id2, e)
	}
	var n int
	_ = s.DB.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE capture=$1", id).Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
	_ = s.DB.QueryRow(ctx, "SELECT count(*) FROM fetches WHERE capture=$1", id).Scan(&n)
	if n != 2 {
		t.Fatal(n)
	}
	_, e = s.DB.Exec(ctx, "INSERT INTO jobs(capture) VALUES(-1)")
	if e == nil {
		t.Fatal("missing foreign key")
	}
}
func TestLeaseFence(t *testing.T) {
	s, ns := setup(t)
	ctx := context.Background()
	_, e := s.Accept(ctx, ns, "trips", "", capture(uint64(s.Now().Unix()), "a"), s.Now())
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.ClaimNamespace(ctx, ns)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.DB.Exec(ctx, "UPDATE jobs SET lease_until=now()-interval '1 minute',state='pending' WHERE id=$1", a.ID)
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.ClaimNamespace(ctx, ns)
	if e != nil {
		t.Fatal(e)
	}
	if a.Token == b.Token {
		t.Fatal("token not advanced")
	}
	if e = s.Process(ctx, a); e == nil {
		t.Fatal("stale worker accepted")
	}
	if e = s.Process(ctx, b); e != nil {
		t.Fatal(e)
	}
}
func TestQuarantine(t *testing.T) {
	s, ns := setup(t)
	for _, raw := range [][]byte{{1, 2, 3}, capture(uint64(s.Now().Add(time.Hour).Unix()), "a")} {
		id := process(t, s, ns, raw)
		var outcome string
		_ = s.DB.QueryRow(context.Background(), "SELECT outcome FROM outcomes WHERE capture=$1", id).Scan(&outcome)
		if outcome != "quarantined" {
			t.Fatal(outcome)
		}
	}
}
func TestReplayDeterministicAndIsolated(t *testing.T) {
	s, ns := setup(t)
	now := uint64(s.Now().Unix())
	b := capture(now, "a")
	process(t, s, ns, b)
	process(t, s, ns+"other", b)
	a, e := s.Fingerprint(context.Background(), ns)
	if e != nil {
		t.Fatal(e)
	}
	other, e := s.Fingerprint(context.Background(), ns+"other")
	if e != nil || a != other {
		t.Fatal(a, other, e)
	}
}
func TestFreshnessClockAndBounds(t *testing.T) {
	now := time.Unix(1000, 0)
	for _, c := range []struct {
		ts   int64
		want string
	}{{1000, "fresh"}, {910, "fresh"}, {909, "stale"}, {1031, "unavailable"}} {
		if got := Freshness(now, &c.ts); got != c.want {
			t.Fatal(got, c)
		}
	}
	if Freshness(now, nil) != "unavailable" {
		t.Fatal("missing timestamp")
	}
	for _, s := range []string{"24:05:00", "48:00:01"} {
		if _, e := ParseTime(s); e != nil {
			t.Fatal(e)
		}
	}
	for _, s := range []string{"-1:00:00", "25:61:00", "a:b:c", "168:00:00"} {
		if _, e := ParseTime(s); e == nil {
			t.Fatal(s)
		}
	}
}
func TestServiceDateDST(t *testing.T) {
	loc, _ := time.LoadLocation("America/Los_Angeles")
	d := time.Date(2026, 11, 14, 0, 0, 0, 0, loc)
	at := ServiceTime(d, 25*3600, loc)
	if at.Day() != 15 || at.Hour() != 1 {
		t.Fatal(at)
	}
	spring := time.Date(2026, 3, 8, 12, 0, 0, 0, loc)
	if ServiceTime(spring, 0, loc).Sub(spring) != -12*time.Hour {
		t.Fatal("GTFS noon anchor")
	}
}
func TestHTTPBoundsAndAuth(t *testing.T) {
	s, _ := setup(t)
	a := &API{Store: s, Token: "test-only"}
	for _, path := range []string{"/v1/departures?limit=1001", "/v1/departures?stop=", "/v1/observations?after=-1"} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 400 {
			t.Fatal(path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("POST", "/v1/admin/requeue?id=1", nil))
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
}
func TestWarmCacheFreshness(t *testing.T) {
	s, ns := setup(t)
	ctx := context.Background()
	now := time.Now()
	s.Now = func() time.Time { return now }
	id := process(t, s, ns, capture(uint64(now.Unix()), "cache-stop"))
	_, e := s.DB.Exec(ctx, "INSERT INTO feed_state(ns,feed,generation,source_time,hash) VALUES('live','trips',$1,$2,'test') ON CONFLICT(ns,feed) DO UPDATE SET generation=$1,source_time=$2", id, now.Unix())
	if e != nil {
		t.Fatal(e)
	}
	a := &API{Store: s, Cache: true}
	get := func() string {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("GET", "/v1/departures?stop=cache-stop", nil))
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		var x map[string]any
		if e = json.Unmarshal(w.Body.Bytes(), &x); e != nil {
			t.Fatal(e)
		}
		return x["status"].(map[string]any)["freshness"].(string)
	}
	if get() != "fresh" {
		t.Fatal("initial")
	}
	now = now.Add(91 * time.Second)
	a.mu.Lock()
	for k, v := range a.cache {
		v.expires = now.Add(time.Hour)
		a.cache[k] = v
	}
	a.mu.Unlock()
	if get() != "stale" {
		t.Fatal("warm cache disguised age")
	}
}
func FuzzDecode(f *testing.F) {
	f.Add(capture(1000, "a"))
	f.Add([]byte{1, 2})
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 1<<20 {
			return
		}
		m, e := Decode(b)
		if e == nil && (m.Header == nil || m.Header.Timestamp == nil) {
			t.Fatal("accepted missing header")
		}
	})
}
func TestDifferentialRejected(t *testing.T) {
	var m gtfs.FeedMessage
	_ = proto.Unmarshal(capture(1000, "a"), &m)
	mode := gtfs.FeedHeader_DIFFERENTIAL
	m.Header.Incrementality = &mode
	b, _ := proto.Marshal(&m)
	_, e := Decode(b)
	if e == nil || !strings.Contains(e.Error(), "unsupported") {
		t.Fatal(e)
	}
}
func TestPruneKeepsPublishedAndUnfinished(t *testing.T) {
	s, ns := setup(t)
	ctx := context.Background()
	old := time.Now().Add(-240 * time.Hour)
	s.Now = func() time.Time { return old }
	superseded := process(t, s, ns, capture(uint64(old.Unix()), "a"))
	current := process(t, s, ns, capture(uint64(old.Unix()+1), "b"))
	refetched := process(t, s, ns, capture(uint64(old.Unix()-5), "d"))
	if _, e := s.Accept(ctx, ns, "trips", "", capture(uint64(old.Unix()-5), "d"), time.Now()); e != nil {
		t.Fatal(e)
	}
	pending, e := s.Accept(ctx, ns, "trips", "", capture(uint64(old.Unix()+2), "c"), old)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Prune(ctx, time.Now().Add(-168*time.Hour), 0); e == nil {
		t.Fatal("accepted zero batch")
	}
	if _, e = s.Prune(ctx, time.Now().Add(-168*time.Hour), 1); e != nil {
		t.Fatal(e)
	}
	left := map[int64]bool{}
	rows, e := s.DB.Query(ctx, "SELECT id FROM captures WHERE ns=$1", ns)
	if e != nil {
		t.Fatal(e)
	}
	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
		left[id] = true
	}
	if left[superseded] || !left[current] || !left[pending] || !left[refetched] {
		t.Fatalf("left %v superseded %d current %d pending %d refetched %d", left, superseded, current, pending, refetched)
	}
	var n int
	for _, q := range []string{"observations WHERE generation", "outcomes WHERE capture", "jobs WHERE capture", "fetches WHERE capture"} {
		if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM "+q+"=$1", superseded).Scan(&n); e != nil || n != 0 {
			t.Fatal(q, n, e)
		}
	}
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM observations WHERE generation=$1", current).Scan(&n); e != nil || n != 1 {
		t.Fatal("current observations", n, e)
	}
}
func TestGenerationIDsSurviveRestore(t *testing.T) {
	s, ns := setup(t)
	ctx := context.Background()
	now := s.Now().Unix()
	backedUp := process(t, s, ns, capture(uint64(now), "a"))
	lost := process(t, s, ns, capture(uint64(now+1), "b"))
	if lost <= backedUp {
		t.Fatalf("ids not increasing: %d then %d", backedUp, lost)
	}
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, "UPDATE feed_state SET generation=$1,source_time=$2 WHERE ns=$3", backedUp, now, ns); e != nil {
		t.Fatal(e)
	}
	for _, q := range []string{
		"DELETE FROM observations WHERE generation=$1",
		"DELETE FROM outcomes WHERE capture=$1",
		"DELETE FROM jobs WHERE capture=$1",
		"DELETE FROM fetches WHERE capture=$1",
		"DELETE FROM captures WHERE id=$1",
	} {
		if _, e = tx.Exec(ctx, q, lost); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = tx.Exec(ctx, "SELECT setval('captures_id_seq', $1)", backedUp); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	next := process(t, s, ns, capture(uint64(now+2), "c"))
	if next <= lost {
		t.Fatalf("generation %d reissued after restore (lost %d)", next, lost)
	}
	var gen int64
	if e = s.DB.QueryRow(ctx, "SELECT generation FROM feed_state WHERE ns=$1", ns).Scan(&gen); e != nil || gen != next {
		t.Fatalf("published %d, want %d: %v", gen, next, e)
	}
	if next >= 1<<53 {
		t.Fatalf("generation %d exceeds JSON-safe integer range", next)
	}
}
