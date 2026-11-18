package transit

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"
)

var Routes = []string{"/healthz", "/readyz", "/v1/feed-status", "/v1/departures", "/v1/observations", "/v1/ops", "/v1/admin/requeue", "/v1/activity"}

type Departure struct {
	Trip      string `json:"trip"`
	Stop      string `json:"stop"`
	Time      int64  `json:"time"`
	EventTime *int64 `json:"event_time"`
	Kind      string `json:"kind"`
}
type Status struct {
	Generation *int64     `json:"generation"`
	Source     *int64     `json:"source_time"`
	Fetched    *time.Time `json:"fetched_at"`
	Processed  *time.Time `json:"processed_at"`
	Freshness  string     `json:"freshness"`
}
type entry struct {
	rows    []Departure
	expires time.Time
}
type API struct {
	Store  *Store
	Cache  bool
	Token  string
	Origin string
	mu     sync.Mutex
	cache  map[string]entry
}

func (s *Store) Status(ctx context.Context) (Status, error) {
	var st Status
	e := s.DB.QueryRow(ctx, `SELECT f.generation,f.source_time,c.fetched_at,o.processed_at FROM feed_state f JOIN captures c ON c.id=f.generation LEFT JOIN outcomes o ON o.capture=c.id WHERE f.ns='live' AND f.feed='trips'`).Scan(&st.Generation, &st.Source, &st.Fetched, &st.Processed)
	if errors.Is(e, pgx.ErrNoRows) {
		e = nil
	}
	st.Freshness = Freshness(s.Now(), st.Source)
	return st, e
}
func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := make([]byte, 12)
	_, _ = rand.Read(id)
	w.Header().Set("X-Request-ID", hex.EncodeToString(id))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if a.Origin != "" && r.Header.Get("Origin") == a.Origin {
		w.Header().Set("Access-Control-Allow-Origin", a.Origin)
		w.Header().Set("Vary", "Origin")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	fail := func(code int, msg string) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
	}
	if r.Method == "GET" && r.URL.Path == "/healthz" {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}
	if r.Method == "GET" && r.URL.Path == "/readyz" {
		if e := a.Store.DB.Ping(ctx); e != nil {
			fail(503, "database unavailable")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
		return
	}
	if r.Method == "POST" && r.URL.Path == "/v1/admin/requeue" {
		if a.Token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+a.Token)) != 1 {
			fail(401, "unauthorized")
			return
		}
		id, e := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		if e != nil {
			fail(400, "invalid id")
			return
		}
		tag, e := a.Store.DB.Exec(ctx, "UPDATE jobs SET state='pending',attempts=0,available_at=$2 WHERE id=$1 AND state='failed'", id, a.Store.Now())
		if e != nil {
			fail(503, "database unavailable")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]int64{"requeued": tag.RowsAffected()})
		return
	}
	if r.URL.Path == "/v1/activity" {
		a.serveActivity(w, r, fail)
		return
	}
	if r.Method != "GET" {
		fail(405, "method not allowed")
		return
	}
	if r.URL.Path == "/v1/ops" {
		o, e := a.Store.Ops(ctx)
		if e != nil {
			fail(503, "database unavailable")
			return
		}
		_ = json.NewEncoder(w).Encode(o)
		return
	}
	if r.URL.Path == "/v1/feed-status" {
		st, e := a.Store.Status(ctx)
		if e != nil {
			fail(503, "database unavailable")
			return
		}
		_ = json.NewEncoder(w).Encode(st)
		return
	}
	limit := 20
	if q := r.URL.Query().Get("limit"); q != "" {
		v, e := strconv.Atoi(q)
		if e != nil || v < 1 || v > 100 {
			fail(400, "limit must be 1..100")
			return
		}
		limit = v
	}
	if r.URL.Path == "/v1/observations" {
		after := int64(0)
		if q := r.URL.Query().Get("after"); q != "" {
			v, e := strconv.ParseInt(q, 10, 64)
			if e != nil || v < 0 {
				fail(400, "invalid cursor")
				return
			}
			after = v
		}
		rows, e := a.Store.DB.Query(ctx, `SELECT id,hash,source_time,fetched_at FROM captures WHERE ns='live' AND id>$1 AND fetched_at>=$2 AND fetched_at<=$3 ORDER BY id LIMIT $4`, after, a.Store.Now().Add(-time.Hour), a.Store.Now(), limit)
		if e != nil {
			fail(503, "database unavailable")
			return
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id int64
			var h string
			var st *int64
			var ft time.Time
			if e = rows.Scan(&id, &h, &st, &ft); e != nil {
				fail(503, "database unavailable")
				return
			}
			out = append(out, map[string]any{"id": id, "hash": h, "source_time": st, "fetched_at": ft})
		}
		if rows.Err() != nil {
			fail(503, "database unavailable")
			return
		}
		_ = json.NewEncoder(w).Encode(out)
		return
	}
	if r.URL.Path != "/v1/departures" {
		fail(404, "not found")
		return
	}
	stop := r.URL.Query().Get("stop")
	if len(stop) == 0 || len(stop) > 128 {
		fail(400, "invalid stop")
		return
	}
	st, e := a.Store.Status(ctx)
	if e != nil {
		fail(503, "database unavailable")
		return
	}
	key := fmt.Sprintf("%v/%s/%d", st.Generation, stop, limit)
	if st.Generation != nil {
		key = fmt.Sprintf("%d/%s/%d", *st.Generation, stop, limit)
	}
	a.mu.Lock()
	cached, ok := a.cache[key]
	a.mu.Unlock()
	now := a.Store.Now()
	out := cached.rows
	// Once the first cached prediction passes, a later trip may enter the limited result.
	firstHasPassed := len(out) > 0 && out[0].Time < now.Unix()
	if !a.Cache || !ok || !cached.expires.After(now) || firstHasPassed {
		out = []Departure{}
		if st.Generation != nil {
			rows, e := a.Store.DB.Query(ctx, `SELECT trip,stop,predicted,event_time FROM observations WHERE generation=$1 AND stop=$2 AND predicted>=$3 ORDER BY predicted,trip LIMIT $4`, *st.Generation, stop, now.Unix(), limit)
			if e != nil {
				fail(503, "database unavailable")
				return
			}
			for rows.Next() {
				var d Departure
				if e = rows.Scan(&d.Trip, &d.Stop, &d.Time, &d.EventTime); e != nil {
					rows.Close()
					fail(503, "database unavailable")
					return
				}
				d.Kind = "prediction"
				out = append(out, d)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				fail(503, "database unavailable")
				return
			}
		}
		if a.Cache {
			a.mu.Lock()
			if a.cache == nil || len(a.cache) >= 256 {
				a.cache = map[string]entry{}
			}
			a.cache[key] = entry{out, now.Add(10 * time.Second)}
			a.mu.Unlock()
		}
	}
	// Freshness is derived on every response, even on a warm cache hit.
	visible := []Departure{}
	for _, d := range out {
		event := d.EventTime
		if event == nil {
			event = st.Source
		}
		d.Kind = Freshness(now, event)
		if st.Freshness != "fresh" {
			d.Kind = st.Freshness
		}
		if d.Time >= now.Unix() {
			visible = append(visible, d)
		}
	}
	if len(visible) == 0 || st.Freshness != "fresh" {
		scheduled, err := a.Store.Scheduled(ctx, stop, limit, now)
		if err != nil {
			fail(503, "database unavailable")
			return
		}
		if len(scheduled) > 0 {
			visible = scheduled
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"status": st, "departures": visible})
}
func (s *Store) Scheduled(ctx context.Context, stop string, limit int, now time.Time) ([]Departure, error) {
	out := []Departure{}
	var version, zone string
	e := s.DB.QueryRow(ctx, "SELECT id,timezone FROM schedules ORDER BY imported_at DESC,id LIMIT 1").Scan(&version, &zone)
	if errors.Is(e, pgx.ErrNoRows) {
		return out, nil
	}
	if e != nil {
		return nil, e
	}
	loc, e := time.LoadLocation(zone)
	if e != nil {
		return nil, e
	}
	local := now.In(loc)
	for back := 0; back < 7; back++ {
		date := local.AddDate(0, 0, -back)
		day := date.Format("2006-01-02")
		rows, e := s.DB.Query(ctx, `SELECT t.id,st.departure FROM stop_times st JOIN trips t ON (t.version,t.id)=(st.version,st.trip) JOIN calendars c ON (c.version,c.id)=(t.version,t.service) LEFT JOIN calendar_dates cd ON (cd.version,cd.service,cd.date)=(c.version,c.id,$3::date) WHERE st.version=$1 AND st.stop=$2 AND COALESCE(cd.added,($3::date BETWEEN c.start_date AND c.end_date AND c.weekdays[$4])) ORDER BY st.departure,t.id LIMIT 1000`, version, stop, day, int(date.Weekday())+1)
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			var trip string
			var sec int
			if e = rows.Scan(&trip, &sec); e != nil {
				rows.Close()
				return nil, e
			}
			at := ServiceTime(date, sec, loc).Unix()
			if at >= now.Unix() {
				out = append(out, Departure{Trip: trip, Stop: stop, Time: at, Kind: "scheduled"})
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Time == out[j].Time {
			return out[i].Trip < out[j].Trip
		}
		return out[i].Time < out[j].Time
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

type Ops struct {
	Status   Status           `json:"status"`
	Jobs     map[string]int64 `json:"jobs"`
	Outcomes map[string]int64 `json:"outcomes"`
	Schedule *string          `json:"schedule"`
}

func (s *Store) Ops(ctx context.Context) (Ops, error) {
	o := Ops{Jobs: map[string]int64{}, Outcomes: map[string]int64{}}
	st, e := s.Status(ctx)
	if e != nil {
		return o, e
	}
	o.Status = st
	count := func(q string, into map[string]int64) error {
		rows, e := s.DB.Query(ctx, q)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var k string
			var n int64
			if e := rows.Scan(&k, &n); e != nil {
				return e
			}
			into[k] = n
		}
		return rows.Err()
	}
	if e = count("SELECT j.state,count(*) FROM jobs j JOIN captures c ON c.id=j.capture WHERE c.ns='live' GROUP BY 1", o.Jobs); e != nil {
		return o, e
	}
	if e = count("SELECT o.outcome,count(*) FROM outcomes o JOIN captures c ON c.id=o.capture WHERE c.ns='live' GROUP BY 1", o.Outcomes); e != nil {
		return o, e
	}
	e = s.DB.QueryRow(ctx, "SELECT id FROM schedules ORDER BY imported_at DESC,id LIMIT 1").Scan(&o.Schedule)
	if errors.Is(e, pgx.ErrNoRows) {
		e = nil
	}
	return o, e
}
