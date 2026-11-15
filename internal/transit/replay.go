package transit

import (
	"context"
	"encoding/json"
)

func (s *Store) ClaimNamespace(ctx context.Context, ns string) (Job, error) {
	var j Job
	e := s.DB.QueryRow(ctx, `WITH candidate AS (SELECT j.id FROM jobs j JOIN captures c ON c.id=j.capture WHERE c.ns=$2 AND j.state='pending' ORDER BY j.id FOR UPDATE OF j SKIP LOCKED LIMIT 1) UPDATE jobs SET state='running',token=token+1,attempts=attempts+1,lease_until=$1::timestamptz+interval '30 seconds' FROM candidate WHERE jobs.id=candidate.id RETURNING jobs.id,capture,token`, s.Now(), ns).Scan(&j.ID, &j.Capture, &j.Token)
	return j, e
}
func (s *Store) Fingerprint(ctx context.Context, ns string) (string, error) {
	rows, e := s.DB.Query(ctx, `SELECT o.entity,o.sequence,o.trip,o.stop,o.predicted,o.event_time FROM observations o JOIN feed_state f ON f.generation=o.generation WHERE f.ns=$1 ORDER BY o.entity,o.sequence`, ns)
	if e != nil {
		return "", e
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		v, e := rows.Values()
		if e != nil {
			return "", e
		}
		out = append(out, v)
	}
	if rows.Err() != nil {
		return "", rows.Err()
	}
	b, e := json.Marshal(out)
	return Hash(b), e
}
