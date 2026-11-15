package transit

import (
	"context"
	"errors"
	"time"
)

// Prune deletes captures last fetched before cutoff, keeping published generations and unfinished jobs.
func (s *Store) Prune(ctx context.Context, before time.Time, batch int) (int64, error) {
	if batch < 1 || batch > 10000 {
		return 0, errors.New("batch must be 1..10000")
	}
	var total int64
	for {
		n, e := s.pruneBatch(ctx, before, batch)
		total += n
		if e != nil || n < int64(batch) {
			return total, e
		}
	}
}

func (s *Store) pruneBatch(ctx context.Context, before time.Time, batch int) (int64, error) {
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback(ctx)
	var ids []int64
	e = tx.QueryRow(ctx, `SELECT coalesce(array_agg(id),'{}') FROM (SELECT c.id FROM captures c
		WHERE c.fetched_at<$1
		AND NOT EXISTS (SELECT 1 FROM fetches f WHERE f.capture=c.id AND f.fetched_at>=$1)
		AND NOT EXISTS (SELECT 1 FROM feed_state s WHERE s.generation=c.id)
		AND NOT EXISTS (SELECT 1 FROM jobs j WHERE j.capture=c.id AND j.state IN ('pending','running'))
		ORDER BY c.id LIMIT $2 FOR UPDATE SKIP LOCKED) d`, before, batch).Scan(&ids)
	if e != nil || len(ids) == 0 {
		return 0, e
	}
	for _, q := range []string{
		"DELETE FROM observations WHERE generation=ANY($1)",
		"DELETE FROM outcomes WHERE capture=ANY($1)",
		"DELETE FROM jobs WHERE capture=ANY($1)",
		"DELETE FROM fetches WHERE capture=ANY($1)",
		"DELETE FROM captures WHERE id=ANY($1)",
	} {
		if _, e = tx.Exec(ctx, q, ids); e != nil {
			return 0, e
		}
	}
	return int64(len(ids)), tx.Commit(ctx)
}
