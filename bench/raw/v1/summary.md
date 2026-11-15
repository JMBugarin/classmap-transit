| rep | variant | departures p50 / p95 / p99 (ms) | all p95 (ms) | errors | dropped | achieved rps | plan |
|---|---|---|---|---|---|---|---|
| 0 | uncached | 5000 / 5001 / 5002 | 5001 | 45.19% | 3773 | 93.0 | Bitmap Index Scan on feed_state_pkey, Parallel Seq Scan on observations (29.13 ms) |
| 0 | indexed | 2.02 / 2.62 / 3.12 | 2.53 | 0.00% | 0 | 100.0 | Index Scan using observations_stop, Seq Scan on feed_state (0.14 ms) |
| 0 | cached | 1.52 / 2.36 / 2.74 | 2.27 | 0.00% | 0 | 100.0 | Index Scan using observations_stop, Seq Scan on feed_state (0.14 ms) |
| 1 | uncached | 5000 / 5002 / 5002 | 5001 | 55.90% | 3885 | 92.8 | Parallel Seq Scan on observations, Seq Scan on feed_state (27.22 ms) |
| 1 | indexed | 2.08 / 2.72 / 3.20 | 2.61 | 0.00% | 0 | 100.0 | Index Scan using observations_stop, Seq Scan on feed_state (0.15 ms) |
| 1 | cached | 1.51 / 2.37 / 2.71 | 2.30 | 0.00% | 0 | 100.0 | Index Scan using observations_stop, Seq Scan on feed_state (0.14 ms) |
| 2 | uncached | 5000 / 5002 / 5002 | 5001 | 58.22% | 3810 | 92.9 | Parallel Seq Scan on observations, Seq Scan on feed_state (26.97 ms) |
| 2 | indexed | 2.08 / 2.57 / 3.00 | 2.50 | 0.00% | 0 | 100.0 | Index Scan using observations_stop, Seq Scan on feed_state (0.14 ms) |
| 2 | cached | 1.63 / 2.71 / 3.31 | 2.56 | 0.00% | 0 | 100.0 | Index Scan using observations_stop, Seq Scan on feed_state (0.17 ms) |

| variant | departures p95 min–max (ms) | departures p99 min–max (ms) | errors max | dropped total |
|---|---|---|---|---|
| uncached (3 runs) | 5001–5002 | 5002–5002 | 58.22% | 11468 |
| indexed (3 runs) | 2.57–2.72 | 3.00–3.20 | 0.00% | 0 |
| cached (3 runs) | 2.36–2.71 | 2.71–3.31 | 0.00% | 0 |
