package transit

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
)

const BenchStops = 760

func BenchStop(i int) string { return fmt.Sprintf("S%04d", i) }

// SeedBench publishes one synthetic generation with n observations. It refuses any database not named *_bench.
func (s *Store) SeedBench(ctx context.Context, n int, seed uint64, at time.Time) (int64, error) {
	var db string
	if e := s.DB.QueryRow(ctx, "SELECT current_database()").Scan(&db); e != nil {
		return 0, e
	}
	if len(db) < 7 || db[len(db)-6:] != "_bench" {
		return 0, errors.New("bench seeding requires a database named *_bench")
	}
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SET LOCAL statement_timeout = 0"); e != nil {
		return 0, e
	}
	if _, e = tx.Exec(ctx, "TRUNCATE observations, outcomes, jobs, fetches, feed_state, captures RESTART IDENTITY CASCADE"); e != nil {
		return 0, e
	}
	var id int64
	raw := []byte(fmt.Sprintf("synthetic-bench rows=%d seed=%d", n, seed))
	if e = tx.QueryRow(ctx, "INSERT INTO captures(ns,feed,hash,raw,source_time,fetched_at,decoder) VALUES('live','trips',$1,$2,$3,$4,'synthetic-bench') RETURNING id", Hash(raw), raw, at.Unix(), at).Scan(&id); e != nil {
		return 0, e
	}
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	zipf := rand.NewZipf(r, 1.1, 1, BenchStops-1)
	base := at.Unix()
	rows := 0
	src := pgx.CopyFromFunc(func() ([]any, error) {
		if rows == n {
			return nil, nil
		}
		i := rows
		rows++
		trip := fmt.Sprintf("T%06d", i/40)
		return []any{id, fmt.Sprintf("E%06d", i/40), int32(i % 40), trip, BenchStop(int(zipf.Uint64())), base + r.Int64N(86400), base}, nil
	})
	if _, e = tx.CopyFrom(ctx, pgx.Identifier{"observations"}, []string{"generation", "entity", "sequence", "trip", "stop", "predicted", "event_time"}, src); e != nil {
		return 0, e
	}
	if _, e = tx.Exec(ctx, "INSERT INTO outcomes VALUES($1,'published',$2)", id, at); e != nil {
		return 0, e
	}
	if _, e = tx.Exec(ctx, "INSERT INTO feed_state(ns,feed,generation,source_time,hash) VALUES('live','trips',$1,$2,$3)", id, at.Unix(), Hash(raw)); e != nil {
		return 0, e
	}
	return id, tx.Commit(ctx)
}
