# Separate-host benchmark v2 — results

Protocol: `PROTOCOL-v2.md`, frozen in `11484a8` before any load was sent. Raw output: `bench/raw/v2/` (k6 summaries, query plans, start and finish stamps, host and load-machine environment, CloudWatch metrics). Transfer check: the result tar's sha256 was `42dade90…7161e6` on the load machine, on the host and after copying here.

**Setup as run.** The server was EC2 t4g.small `i-061380374c32f3c5b` (us-west-2, 2 vCPU Graviton, 2 GiB, standard credits) running image `dffd820` and PostgreSQL 17.12, with the live API and poller running alongside. The benchmark used a separate `transit_bench` database seeded with 1,000,000 rows over 760 stops (19 s). The load machine was a Windows 11 laptop (Ryzen 5 7640HS, on AC power in Best performance mode) wired over 1 Gbps Ethernet on a home connection, running k6 2.4.0 with the unchanged `k6.js` against `http://<host>:18080`. Port 18080 was open only to the load machine's public address, and only during the run. The run lasted from 11:25:29Z to 13:01:14Z on 2026-11-17.

## Results

| rep | variant | departures p50 / p95 / p99 (ms) | all p95 (ms) | errors | dropped | achieved rps | plan |
|---|---|---|---|---|---|---|---|
| 0 | uncached | 5032 / 5037 / 5042 | 5036 | 70.04% | 5082 | 90.8 | Parallel Seq Scan on observations (106 ms) |
| 0 | indexed | 31.55 / 36.20 / 40.32 | 36.05 | 0.00% | 0 | 100.0 | Index Scan using observations_stop (0.92 ms) |
| 0 | cached | 31.63 / 36.59 / 41.16 | 36.40 | 0.00% | 0 | 100.0 | Index Scan using observations_stop (0.31 ms) |
| 1 | uncached | 5032 / 5038 / 5043 | 5037 | 70.05% | 5021 | 90.9 | Parallel Seq Scan on observations (108 ms) |
| 1 | indexed | 32.01 / 36.59 / 40.95 | 36.40 | 0.00% | 0 | 100.0 | Index Scan using observations_stop (0.30 ms) |
| 1 | cached | 31.09 / 35.84 / 39.98 | 35.70 | 0.00% | 0 | 100.0 | Index Scan using observations_stop (0.31 ms) |
| 2 | uncached | 5087 / 5131 / 5140 | 5128 | 70.06% | 8358 | 85.3 | Parallel Seq Scan on observations (119 ms) |
| 2 | indexed | 31.70 / 36.44 / 41.22 | 36.20 | 0.00% | 0 | 100.0 | Index Scan using observations_stop (0.34 ms) |
| 2 | cached | 31.06 / 36.64 / 41.75 | 36.53 | 0.00% | 0 | 100.0 | Index Scan using observations_stop (0.33 ms) |

| variant | departures p95 min–max (ms) | departures p99 min–max (ms) | errors max | dropped total |
|---|---|---|---|---|
| uncached (3 runs) | 5037–5131 | 5042–5140 | 70.06% | 18,461 |
| indexed (3 runs) | 36.20–36.59 | 40.32–41.22 | 0.00% | 0 |
| cached (3 runs) | 35.84–36.64 | 39.98–41.75 | 0.00% | 0 |

**Gate** (100 requests/s for 10 minutes, p95 under 200 ms, errors under 1%, in every repeat):
- **indexed:** met in 3 of 3 runs.
- **cached:** met in 3 of 3 runs.
- **uncached:** missed in 3 of 3 runs.

## Reading

- **Without the stop index, the service can't serve this load.** Each departures query runs a parallel sequential scan over 1M rows and holds the host's 2 vCPUs near 99%. Departures queue up until the 5 s statement timeout. The failure rate (70%) matches the departures share of the mix: feed-status and observations still succeeded. k6 hit its 400-VU cap and dropped 5,021–8,358 iterations per run. This is the same failure seen in v1, now on the deployed host.
- **With the index, departures p95 is about 36 ms from the load machine.** The plan executes in about 0.3 ms on the host, so almost all of the observed latency is the home-to-us-west-2 network path and HTTP handling, not the database. The round-trip time wasn't measured separately.
- **The 10 s response cache made no measurable difference over the index alone** at this rate and request mix (p95 35.84–36.64 vs 36.20–36.59 ms). Network latency dominates both.

## Host CPU credits (CloudWatch, 5-minute periods, `raw/v2/cloudwatch.json`)

- CPUCreditBalance started at 28.7 and fell during each uncached run. It reached **0 at 12:45–12:50**, during rep 2 uncached, so that run was throttled to baseline for its later part. That is consistent with its higher latency and more dropped iterations than reps 0–1.
- Rep 2 indexed (12:50–13:01) started with an empty credit balance. At about 11–14% average CPU it stayed under the instance's baseline, and its results match the other indexed runs.
- CPU averaged about 10–12% during indexed and cached runs and about 98–99% during uncached runs. Surplus credits stayed at 0 (standard mode, no extra charge).

## Deviations and limits

- **First launch on the load machine failed before sending any load:** k6 wasn't on the launching shell's PATH. It's kept in `raw/v2-aborted-k6-path/`. The run was relaunched unchanged one minute later.
- **One home network path, one day, one region.** Latency includes the WAN round trip and isn't comparable to v1's same-machine 2.4–2.7 ms.
- **The live API and poller shared the host and its credits during the run.** Their load is small, about 3% CPU idle before the run.
- **The load machine's earlier environment record lists Wi-Fi.** It was taken before Ethernet was connected. The update in the same file shows Ethernet as the active route at run time.
- **Afterwards:** port 18080 was closed, the load machine's restricted SSH key was removed, the bench container was stopped, and `transit_bench` was dropped (2026-11-17 ~17:40Z).
