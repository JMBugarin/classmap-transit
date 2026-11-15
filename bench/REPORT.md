# Controlled local benchmark v1: results

Protocol: [PROTOCOL.md](PROTOCOL.md), frozen in ded5906 before the measured run. Raw output: `raw/v1/` (k6 text and JSON summaries, warmups, query plans, API logs, environment, start times, `summary.json`). Generated with `python3 bench/summarize.py bench/raw/v1`.

Run: 2026-11-15 07:41–09:17 UTC, code at ded5906 with a clean tree. Apple M5 Pro (18 cores, 24 GB), Go 1.27.4, k6 2.4.0, PostgreSQL 17.12 in Docker on a 4-vCPU Colima VM. API, database and load generator all ran on the same laptop. These are **exploratory same-machine results**: they don't meet the separate load-generator gate, and they say nothing about a deployed service.

## Every run

| rep | variant | departures p50 / p95 / p99 (ms) | all-endpoint p95 (ms) | errors | dropped | achieved rps | plan for one hot stop |
|---|---|---|---|---|---|---|---|
| 0 | uncached | 5000 / 5001 / 5002 | 5001 | 45.19% | 3773 | 93.0 | Parallel Seq Scan on observations (29.1 ms) |
| 0 | indexed | 2.02 / 2.62 / 3.12 | 2.53 | 0.00% | 0 | 100.0 | Index Scan using observations_stop (0.14 ms) |
| 0 | cached | 1.52 / 2.36 / 2.74 | 2.27 | 0.00% | 0 | 100.0 | Index Scan using observations_stop (0.14 ms) |
| 1 | indexed | 2.08 / 2.72 / 3.20 | 2.61 | 0.00% | 0 | 100.0 | Index Scan using observations_stop (0.15 ms) |
| 1 | cached | 1.51 / 2.37 / 2.71 | 2.30 | 0.00% | 0 | 100.0 | Index Scan using observations_stop (0.14 ms) |
| 1 | uncached | 5000 / 5002 / 5002 | 5001 | 55.90% | 3885 | 92.8 | Parallel Seq Scan on observations (27.2 ms) |
| 2 | cached | 1.63 / 2.71 / 3.31 | 2.56 | 0.00% | 0 | 100.0 | Index Scan using observations_stop (0.17 ms) |
| 2 | uncached | 5000 / 5002 / 5002 | 5001 | 58.22% | 3810 | 92.9 | Parallel Seq Scan on observations (27.0 ms) |
| 2 | indexed | 2.08 / 2.57 / 3.00 | 2.50 | 0.00% | 0 | 100.0 | Index Scan using observations_stop (0.14 ms) |

## Across repeats

| variant | departures p95 min–max | departures p99 min–max | error rate max | dropped total |
|---|---|---|---|---|
| uncached | 5001–5002 ms | 5002 ms | 58.22% | 11,468 |
| indexed | 2.57–2.72 ms | 3.00–3.20 ms | 0% | 0 |
| cached | 2.36–2.71 ms | 2.71–3.31 ms | 0% | 0 |

## Findings

- **Without the stop index, the API can't sustain 100 requests/s.** One hot-stop query takes about 27–29 ms as a parallel sequential scan over 1M rows. At 70 departures/s against 8 pool connections, requests queue until they hit the 5 s PostgreSQL statement timeout. 45–58% of all requests failed, and 3,773–3,885 iterations per run were dropped once k6 reached its 400-VU cap. Feed-status and observations don't touch that scan, but they also slowed to about 2.4 s p50, because they wait for the same saturated pool.
- **With the index**, the plan becomes an index scan (0.14–0.17 ms), and every run held 100 requests/s with 0 errors, 0 drops and departures p95 of 2.57–2.72 ms.
- **The response cache adds little on top of the index**: departures p50 is 1.51–1.63 ms against 2.02–2.08 ms, but p95 ranges overlap (2.36–2.71 vs 2.57–2.72 ms). Three repeats can't separate them at the tail. On this machine most of the remaining latency is loopback HTTP and k6 overhead, not the database.

## Deviations and disclosures

- The protocol says each request comes from a fixed hash of iteration and VU, "so the sequence is the same for every variant." With `constant-arrival-rate`, the iteration-to-VU assignment depends on scheduling, so the exact sequence is not identical across runs. The mix proportions and the Zipf stop distribution are fixed. The order is not.
- An earlier attempt of this run (started 04:25 UTC) was terminated 8 minutes into its first run when the controlling session ended. A local Playwright run also overlapped its start. That partial output is not included, and the full protocol was restarted from scratch at 07:41 UTC.
- The heartbeat UPDATE (every 15 s) and uncontrolled laptop background load ran throughout, as disclosed in the protocol.
- In uncached runs, the warmups were already saturated (for example, 225 dropped iterations in rep 0's warmup). Warmups are kept in `raw/v1` and not reported as results.

## Not established

Behavior on a real network, a separate load host, cloud instances, real METRO traffic patterns, or anything past 10 minutes per run.
