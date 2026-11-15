package transit

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	gtfs "github.com/MobilityData/gtfs-realtime-bindings/golang/gtfs"
	"google.golang.org/protobuf/proto"
)

func TestOpenAPIMatchesRoutes(t *testing.T) {
	f, e := os.Open("../../api/openapi.yaml")
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	spec := []string{}
	in := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "paths:" {
			in = true
			continue
		}
		if in && line != "" && !strings.HasPrefix(line, " ") {
			break
		}
		if in && strings.HasPrefix(line, "  /") {
			spec = append(spec, strings.TrimSuffix(strings.TrimSpace(line), ":"))
		}
	}
	got := slices.Clone(Routes)
	slices.Sort(got)
	slices.Sort(spec)
	if !slices.Equal(got, spec) {
		t.Fatalf("spec %v routes %v", spec, got)
	}
}

func TestFetchBounds(t *testing.T) {
	big := strings.Repeat("x", maxFeedBytes+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fail":
			w.WriteHeader(503)
		case "/big":
			_, _ = w.Write([]byte(big))
		default:
			if !strings.Contains(r.Header.Get("Accept"), "protobuf") {
				w.WriteHeader(406)
				return
			}
			_, _ = w.Write(capture(1000, "a"))
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	if _, e := Fetch(ctx, srv.Client(), srv.URL+"/fail"); e == nil || !strings.Contains(e.Error(), "503") {
		t.Fatal(e)
	}
	if _, e := Fetch(ctx, srv.Client(), srv.URL+"/big"); e == nil {
		t.Fatal("accepted oversize feed")
	}
	b, e := Fetch(ctx, srv.Client(), srv.URL+"/ok")
	if e != nil || len(b) == 0 {
		t.Fatal(e)
	}
}

func TestPollAcceptsCapture(t *testing.T) {
	s, _ := setup(t)
	now := time.Now()
	raw := capture(uint64(now.UnixNano()), "poll-stop")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(raw) }))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Poll(ctx, s, srv.Client(), srv.URL, "", time.Hour, t.Logf)
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var n int
		if e := s.DB.QueryRow(context.Background(), "SELECT count(*) FROM captures WHERE ns='live' AND hash=$1", Hash(raw)).Scan(&n); e != nil {
			t.Fatal(e)
		}
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("poll did not accept")
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	<-done
}

func TestPredictionFreshnessUsesFeedTimeWithoutEventTime(t *testing.T) {
	s, ns := setup(t)
	ctx := context.Background()
	now := time.Now()
	s.Now = func() time.Time { return now }
	m := &gtfs.FeedMessage{Header: &gtfs.FeedHeader{GtfsRealtimeVersion: proto.String("2.0"), Timestamp: proto.Uint64(uint64(now.Unix()))}, Entity: []*gtfs.FeedEntity{{Id: proto.String("e"), TripUpdate: &gtfs.TripUpdate{Trip: &gtfs.TripDescriptor{TripId: proto.String("t")}, StopTimeUpdate: []*gtfs.TripUpdate_StopTimeUpdate{{StopId: proto.String("no-event-stop"), Departure: &gtfs.TripUpdate_StopTimeEvent{Time: proto.Int64(now.Unix() + 120)}}}}}}}
	b, _ := proto.Marshal(m)
	id := process(t, s, ns, b)
	if _, e := s.DB.Exec(ctx, "INSERT INTO feed_state(ns,feed,generation,source_time,hash) VALUES('live','trips',$1,$2,'test') ON CONFLICT(ns,feed) DO UPDATE SET generation=$1,source_time=$2", id, now.Unix()); e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	(&API{Store: s}).ServeHTTP(w, httptest.NewRequest("GET", "/v1/departures?stop=no-event-stop", nil))
	var x Departures
	if e := json.Unmarshal(w.Body.Bytes(), &x); e != nil || len(x.Departures) != 1 || x.Departures[0].Kind != "fresh" {
		t.Fatal(w.Body.String())
	}
	w = httptest.NewRecorder()
	(&API{Store: s}).ServeHTTP(w, httptest.NewRequest("GET", "/v1/ops", nil))
	var o Ops
	if e := json.Unmarshal(w.Body.Bytes(), &o); e != nil || o.Status.Freshness != "fresh" || o.Jobs == nil {
		t.Fatal(w.Body.String())
	}
}

type Departures struct {
	Status     Status      `json:"status"`
	Departures []Departure `json:"departures"`
}

func TestImportRealMetroSchedule(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	raw, e := os.ReadFile("../../fixtures/metro-static.zip")
	if e != nil {
		t.Fatal(e)
	}
	started := time.Now()
	id, e := s.ImportSchedule(ctx, raw)
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("import %s in %s", id[:12], time.Since(started))
	if again, e := s.ImportSchedule(ctx, raw); e != nil || again != id {
		t.Fatal("re-import not idempotent", e)
	}
	var stopTimes, trips int
	if e = s.DB.QueryRow(ctx, "SELECT (SELECT count(*) FROM stop_times WHERE version=$1),(SELECT count(*) FROM trips WHERE version=$1)", id).Scan(&stopTimes, &trips); e != nil {
		t.Fatal(e)
	}
	if stopTimes != 235038 || trips != 6362 {
		t.Fatal(stopTimes, trips)
	}
	var stop string
	if e = s.DB.QueryRow(ctx, "SELECT stop FROM stop_times WHERE version=$1 GROUP BY stop ORDER BY count(*) DESC, stop LIMIT 1", id).Scan(&stop); e != nil {
		t.Fatal(e)
	}
	loc, _ := time.LoadLocation("America/Los_Angeles")
	at := time.Date(2026, 11, 16, 8, 0, 0, 0, loc)
	got, e := s.Scheduled(ctx, stop, 5, at)
	if e != nil || len(got) == 0 {
		t.Fatal("no scheduled departures", e)
	}
	for i, d := range got {
		if d.Kind != "scheduled" || d.Time < at.Unix() || (i > 0 && d.Time < got[i-1].Time) {
			t.Fatal(got)
		}
	}
}

func FuzzParseTime(f *testing.F) {
	for _, s := range []string{"00:00:00", "24:05:00", "167:59:59", "1:2", "a:b:c"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		v, e := ParseTime(s)
		if e == nil && (v < 0 || v >= 604800) {
			t.Fatal(v)
		}
	})
}
