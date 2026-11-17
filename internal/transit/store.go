package transit

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	gtfs "github.com/MobilityData/gtfs-realtime-bindings/golang/gtfs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
	"time"
)

//go:embed schema.sql
var schema string

type Store struct {
	DB  *pgxpool.Pool
	Now func() time.Time
}
type Job struct{ ID, Capture, Token int64 }

func Open(ctx context.Context, url string) (*Store, error) {
	c, e := pgxpool.ParseConfig(url)
	if e != nil {
		return nil, e
	}
	c.MaxConns = 8
	c.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	db, e := pgxpool.NewWithConfig(ctx, c)
	return &Store{DB: db, Now: time.Now}, e
}
func (s *Store) Migrate(ctx context.Context) error {
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(570127)"); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, schema); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func Hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func (s *Store) Accept(ctx context.Context, ns, feed, schedule string, raw []byte, at time.Time) (int64, error) {
	if ns == "" || feed != "trips" || len(raw) == 0 || len(raw) > 8<<20 {
		return 0, errors.New("invalid capture")
	}
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback(ctx)
	// Serialize acceptance to enforce the global queue bound.
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(570128)"); e != nil {
		return 0, e
	}
	var count int
	if e = tx.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE state IN ('pending','running')").Scan(&count); e != nil {
		return 0, e
	}
	if count >= 1000 {
		return 0, errors.New("backlog full")
	}
	var msg gtfs.FeedMessage
	var source *int64
	if proto.Unmarshal(raw, &msg) == nil && msg.Header != nil && msg.Header.Timestamp != nil && msg.Header.GetTimestamp() <= 1<<63-1 {
		t := int64(msg.Header.GetTimestamp())
		source = &t
	}
	var id int64
	e = tx.QueryRow(ctx, `INSERT INTO captures(ns,feed,hash,raw,source_time,fetched_at,schedule) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,'')) ON CONFLICT(ns,feed,hash) DO UPDATE SET hash=EXCLUDED.hash RETURNING id`, ns, feed, Hash(raw), raw, source, at, schedule).Scan(&id)
	if e != nil {
		return 0, e
	}
	if _, e = tx.Exec(ctx, "INSERT INTO fetches(capture,fetched_at) VALUES($1,$2)", id, at); e != nil {
		return 0, e
	}
	if _, e = tx.Exec(ctx, "INSERT INTO jobs(capture) VALUES($1) ON CONFLICT DO NOTHING", id); e != nil {
		return 0, e
	}
	return id, tx.Commit(ctx)
}
func (s *Store) Claim(ctx context.Context) (Job, error) {
	var j Job
	e := s.DB.QueryRow(ctx, `WITH candidate AS (SELECT id FROM jobs WHERE capture IN (SELECT id FROM captures WHERE ns='live') AND attempts<5 AND ((state='pending' AND available_at<=$1) OR (state='running' AND lease_until<$1)) ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE jobs SET state='running',token=token+1,attempts=attempts+1,lease_until=$1::timestamptz+interval '30 seconds' FROM candidate WHERE jobs.id=candidate.id RETURNING jobs.id,capture,token`, s.Now()).Scan(&j.ID, &j.Capture, &j.Token)
	return j, e
}
func Decode(raw []byte) (*gtfs.FeedMessage, error) {
	var m gtfs.FeedMessage
	if e := proto.Unmarshal(raw, &m); e != nil {
		return nil, e
	}
	if m.Header == nil || m.Header.GetGtfsRealtimeVersion() != "2.0" || m.Header.Timestamp == nil || m.Header.GetTimestamp() == 0 || m.Header.GetTimestamp() > 1<<63-1 || m.Header.GetIncrementality() != gtfs.FeedHeader_FULL_DATASET {
		return nil, errors.New("unsupported mode/version or missing timestamp")
	}
	seen := map[string]bool{}
	for _, en := range m.Entity {
		if en.GetId() == "" || seen[en.GetId()] || en.GetIsDeleted() {
			return nil, errors.New("invalid full snapshot entity")
		}
		seen[en.GetId()] = true
	}
	return &m, nil
}
func (s *Store) Process(ctx context.Context, j Job) error {
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var ns, feed, hash string
	var raw []byte
	var source *int64
	var token int64
	var state string
	var until time.Time
	e = tx.QueryRow(ctx, `SELECT c.ns,c.feed,c.hash,c.raw,c.source_time,j.token,j.state,j.lease_until FROM jobs j JOIN captures c ON c.id=j.capture WHERE j.id=$1 FOR UPDATE OF j`, j.ID).Scan(&ns, &feed, &hash, &raw, &source, &token, &state, &until)
	if e != nil {
		return e
	}
	if token != j.Token || state != "running" || !until.After(s.Now()) {
		return errors.New("lost lease")
	}
	outcome := "published"
	m, parseErr := Decode(raw)
	if parseErr != nil || source == nil || *source > s.Now().Add(30*time.Second).Unix() {
		outcome = "quarantined"
	} else {
		if _, e = tx.Exec(ctx, "INSERT INTO feed_state(ns,feed) VALUES($1,$2) ON CONFLICT DO NOTHING", ns, feed); e != nil {
			return e
		}
		var old *int64
		var oldHash *string
		if e = tx.QueryRow(ctx, "SELECT source_time,hash FROM feed_state WHERE ns=$1 AND feed=$2 FOR UPDATE", ns, feed).Scan(&old, &oldHash); e != nil {
			return e
		}
		if old != nil && *source < *old {
			outcome = "late"
		}
		if old != nil && *source == *old {
			outcome = "duplicate"
			if oldHash == nil || *oldHash != hash {
				outcome = "conflict"
			}
		}
		if outcome == "published" {
			for _, en := range m.Entity {
				u := en.GetTripUpdate()
				if u == nil {
					continue
				}
				for i, st := range u.StopTimeUpdate {
					ev := st.GetDeparture()
					if ev == nil || ev.Time == nil {
						ev = st.GetArrival()
					}
					rel := st.GetScheduleRelationship()
					if st.GetStopId() == "" || ev == nil || ev.Time == nil || rel == gtfs.TripUpdate_StopTimeUpdate_SKIPPED || rel == gtfs.TripUpdate_StopTimeUpdate_NO_DATA {
						continue
					}
					var event *int64
					if u.Timestamp != nil {
						t := int64(u.GetTimestamp())
						event = &t
					}
					_, e = tx.Exec(ctx, "INSERT INTO observations(generation,entity,sequence,trip,stop,predicted,event_time) VALUES($1,$2,$3,$4,$5,$6,$7)", j.Capture, en.GetId(), i, u.GetTrip().GetTripId(), st.GetStopId(), ev.GetTime(), event)
					if e != nil {
						return e
					}
				}
			}
			if _, e = tx.Exec(ctx, "UPDATE feed_state SET generation=$3,source_time=$4,hash=$5 WHERE ns=$1 AND feed=$2", ns, feed, j.Capture, source, hash); e != nil {
				return e
			}
		}
	}
	if _, e = tx.Exec(ctx, "INSERT INTO outcomes VALUES($1,$2,$3) ON CONFLICT DO NOTHING", j.Capture, outcome, s.Now()); e != nil {
		return e
	}
	state = "done"
	if outcome == "quarantined" {
		state = "quarantined"
	}
	if _, e = tx.Exec(ctx, "UPDATE jobs SET state=$2,error=$3 WHERE id=$1 AND token=$4", j.ID, state, outcome, j.Token); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Store) Retry(ctx context.Context, j Job, cause error) error {
	_, e := s.DB.Exec(ctx, `UPDATE jobs SET state=CASE WHEN attempts>=5 THEN 'failed' ELSE 'pending' END, available_at=$3::timestamptz + (power(2,attempts)+random())*interval '1 second',error=$4 WHERE id=$1 AND token=$2 AND state='running'`, j.ID, j.Token, s.Now(), fmt.Sprintf("%.300s", cause.Error()))
	return e
}
func (s *Store) Worker(ctx context.Context) {
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_, _ = s.DB.Exec(ctx, "UPDATE jobs SET state='failed',error='lease attempts exhausted' WHERE state='running' AND attempts>=5 AND lease_until<$1", s.Now())
			j, e := s.Claim(ctx)
			if errors.Is(e, pgx.ErrNoRows) {
				continue
			}
			if e != nil {
				continue
			}
			if e = s.Process(ctx, j); e != nil {
				_ = s.Retry(ctx, j, e)
			}
		}
	}
}
func Freshness(now time.Time, source *int64) string {
	if source == nil {
		return "unavailable"
	}
	age := now.Unix() - *source
	if age < -30 {
		return "unavailable"
	}
	if age > 90 {
		return "stale"
	}
	return "fresh"
}
