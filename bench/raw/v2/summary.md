| rep | variant | departures p50 / p95 / p99 (ms) | all p95 (ms) | errors | dropped | achieved rps | plan |
|---|---|---|---|---|---|---|---|
| 0 | uncached | 5032 / 5037 / 5042 | 5036 | 70.04% | 5082 | 90.8 | Bitmap Index Scan on feed_state_pkey, Parallel Seq Scan on observations (106 ms) |
| 0 | indexed | 31.55 / 36.20 / 40.32 | 36.05 | 0.00% | 0 | 100.0 | Bitmap Index Scan on feed_state_pkey, Index Scan using observations_stop (0.92 ms) |
| 0 | cached | 31.63 / 36.59 / 41.16 | 36.40 | 0.00% | 0 | 100.0 | Index Scan using observations_stop, Seq Scan on feed_state (0.31 ms) |
| 1 | uncached | 5032 / 5038 / 5043 | 5037 | 70.05% | 5021 | 90.9 | Parallel Seq Scan on observations, Seq Scan on feed_state (108 ms) |
| 1 | indexed | 32.01 / 36.59 / 40.95 | 36.40 | 0.00% | 0 | 100.0 | Index Scan using observations_stop, Seq Scan on feed_state (0.30 ms) |
| 1 | cached | 31.09 / 35.84 / 39.98 | 35.70 | 0.00% | 0 | 100.0 | Index Scan using observations_stop, Seq Scan on feed_state (0.31 ms) |
| 2 | uncached | 5087 / 5131 / 5140 | 5128 | 70.06% | 8358 | 85.3 | Parallel Seq Scan on observations, Seq Scan on feed_state (119 ms) |
| 2 | indexed | 31.70 / 36.44 / 41.22 | 36.20 | 0.00% | 0 | 100.0 | Index Scan using observations_stop, Seq Scan on feed_state (0.34 ms) |
| 2 | cached | 31.06 / 36.64 / 41.75 | 36.53 | 0.00% | 0 | 100.0 | Index Scan using observations_stop, Seq Scan on feed_state (0.33 ms) |

| variant | departures p95 min–max (ms) | departures p99 min–max (ms) | errors max | dropped total |
|---|---|---|---|---|
| uncached (3 runs) | 5037–5131 | 5042–5140 | 70.06% | 18461 |
| indexed (3 runs) | 36.20–36.59 | 40.32–41.22 | 0.00% | 0 |
| cached (3 runs) | 35.84–36.64 | 39.98–41.75 | 0.00% | 0 |
