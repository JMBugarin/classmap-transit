# In-region benchmark v3 — results

Protocol: `PROTOCOL-v3.md`, frozen and pushed in `0c15ac7` before any load was sent. The load instance ran `run-load.sh` from a clone checked out at that commit (`raw/v3/load-git-sha.txt`). Raw output: `bench/raw/v3/` (k6 summaries, query plans, start and finish stamps, host and load-instance environment, network baseline, CloudWatch metrics, run log).

**Setup as run.** The server was EC2 t4g.small `i-061380374c32f3c5b` (us-west-2d, 2 vCPU Graviton, 2 GiB, standard credits) running bench image `dffd820` and PostgreSQL 17.12, with the live API (`c1d2ea0`), poller and Caddy ingress running alongside. The installed `bench-host.sh` matched `bench/remote/host.sh` by sha256. `transit_bench` was seeded with 1,000,000 rows over 760 stops in 19 s.

The load instance was a c7i-flex.large `i-0990869e263d26b83` (2 vCPU, 4 GiB) in the same subnet, running k6 2.4.0 (checksum verified) with the unchanged `k6.js` against the host's private address on port 18080. Port 18080 and SSH were open only to the load instance's security group.

The measured runs went from 05:12:17Z to 06:14:58Z on 2026-11-21.

**Network baseline.** 200 sequential new-connection requests to `/readyz` from the load instance before the first warmup:

- TCP connect: p50 0.29 ms, p95 0.49 ms.
- Whole request: p50 0.93 ms, p95 1.33 ms.

## Results

| rep | variant | departures p50 / p95 / p99 (ms) | all p95 (ms) | errors | dropped | achieved rps | plan for one hot stop |
|---|---|---|---|---|---|---|---|
| 0 | indexed | 1.32 / 1.64 / 2.11 | 1.59 | 0.00% | 0 | 100.0 | Index Scan using observations_stop (0.33 ms) |
| 0 | cached | 1.04 / 1.46 / 1.73 | 1.42 | 0.00% | 0 | 100.0 | Index Scan using observations_stop (0.31 ms) |
| 1 | cached | 1.03 / 1.47 / 1.76 | 1.43 | 0.00% | 0 | 100.0 | Index Scan using observations_stop (0.30 ms) |
| 1 | indexed | 1.35 / 1.65 / 2.15 | 1.61 | 0.00% | 0 | 100.0 | Index Scan using observations_stop (0.32 ms) |
| 2 | indexed | 1.35 / 1.65 / 2.19 | 1.61 | 0.00% | 0 | 100.0 | Index Scan using observations_stop (0.32 ms) |
| 2 | cached | 1.04 / 1.50 / 1.84 | 1.46 | 0.00% | 0 | 100.0 | Index Scan using observations_stop (0.32 ms) |

Rows are in run order. Each measured run sent about 60,000 requests.

| variant | departures p95 min–max (ms) | departures p99 min–max (ms) | errors max | dropped total |
|---|---|---|---|---|
| indexed (3 runs) | 1.64–1.65 | 2.11–2.19 | 0.00% | 0 |
| cached (3 runs) | 1.46–1.50 | 1.73–1.84 | 0.00% | 0 |

**Gate** (100 requests/s for 10 minutes, p95 under 200 ms, errors under 1%, in every repeat):
- **indexed:** met in 3 of 3 runs.
- **cached:** met in 3 of 3 runs.

**Figure for the resume:** the worst indexed departures p95 is 1.65 ms.

## Reading

- **The service's own departures p95 is about 1.65 ms.** From the home connection in v2 it was 36.20–36.59 ms with the same host, image, data and load. About 34.6 ms of that was the network path.
- **What the 1.65 ms is made of.** The query plan runs in 0.30–0.33 ms and a new TCP connection inside the zone takes about 0.3 ms. The rest is HTTP handling, connection-pool checkout and JSON encoding in the API.
- **The response cache is now visible.** It lowers departures p50 from 1.32–1.35 to 1.03–1.04 ms and p95 from 1.64–1.65 to 1.46–1.50 ms, with no overlap across repeats. In v2 the network hid this, and in v1 the loopback and k6 overhead on one laptop made the p95 ranges overlap.
- **The host had headroom.** CPU averaged 9.6–12.0% per 5-minute period during the runs, against 3.3% before and after. CPUCreditBalance stayed at its 576 maximum throughout (`raw/v3/cloudwatch.json`). The load instance averaged about 3.4% CPU, so k6 was not the bottleneck.

## Deviations and limits

- **The plan for the two-row `feed_state` lookup varied.** It was a bitmap index scan in rep 0 and a sequential scan in reps 1–2. The departures scan was the same index scan in every run, and total plan time stayed at 0.30–0.33 ms.
- **The operator's SSH session that launched the run hung.** A background subshell held its output pipe until the 30-minute local timeout. The run itself was detached (`nohup`, own log) and wasn't affected (`raw/v3/run.log`, `progress.txt`).
- **This is a best case for network distance.** The load instance was in the host's subnet, and the bench port bypasses Caddy and TLS, as in v2. Web-map users also pay TLS and their own internet path, which v2's 36 ms better represents.
- **One zone, one day, one host shared with the live service.** Synthetic data, not real traffic.
- **Cleanup on 2026-11-21 06:16–06:20Z:**
  - The bench container stopped, `transit_bench` dropped and the temporary key removed from the host.
  - Both temporary security-group rules revoked.
  - The load instance terminated, with its volume, and its security group deleted.
