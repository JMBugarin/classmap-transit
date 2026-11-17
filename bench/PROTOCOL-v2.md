# Separate-host benchmark protocol v2

Frozen before the run, in the commit that adds this file. Results come in later commits and don't change this file. v1 (`PROTOCOL.md`) ran everything on one laptop. v2 keeps the same data, load and order, and moves the API and database to the deployed EC2 host and k6 to a separate machine.

**Question.** Same as v1: for `/v1/departures` on one published generation of 1,000,000 observations, how do `uncached` (no stop index, no response cache), `indexed` (stop index) and `cached` (stop index plus the 10 s in-process response cache) compare when the load comes over a real network from another machine?

**Data.** `transit bench-seed` with BENCH_ROWS=1000000 and BENCH_SEED=20261115 into a separate `transit_bench` database in the deployed PostgreSQL container. Synthetic stops and trips only, as in v1. The live `transit` database isn't touched.

**Server.** The deployed EC2 t4g.small (us-west-2, 2 vCPU Graviton, 2 GiB, standard CPU credits), image `classmap-transit:dffd820` (the deployed image), PostgreSQL 17.12. A second API container, `transit-bench-api`, runs from that image with `WORKERS=1`, `SKIP_MIGRATE=1` and the default 5 s statement timeout, published on port 18080. The security group opens 18080 only to the load machine's public address for the duration of the run and closes it afterwards. The live API and poller keep running beside it. `bench/remote/host.sh` switches variants on the host. It's reached over SSH with a key that can only run that script.

**Load machine.** A separate Windows 11 laptop (Ryzen 5 7640HS, 6 cores/12 threads, about 15 GiB, on AC power, "Best performance" mode) on a wired home connection. It runs k6 2.4.0 with the unchanged `bench/k6.js` and `BASE_URL` set to the host's public address. Its environment record is copied into the results.

**Load.** Unchanged from v1: k6 `constant-arrival-rate` at 100 requests/s for 600 s (50 preallocated VUs, cap 400), a 70/20/10 mix of departures, feed-status and observations, a deterministic request sequence (SEED 20261115), and a 30 s warmup before each measured run that is kept but not reported.

**Order.** Unchanged: three repeats with rotated variant order (uncached, indexed, cached), (indexed, cached, uncached), (cached, uncached, indexed). The bench API restarts for every run. `ANALYZE observations` runs after each index change, and the query plan for one hot stop is saved before each run.

**Freshness.** Unchanged: a heartbeat on the host sets the bench database's `feed_state.source_time` to now every 15 s.

**Reported.** For every run: departures p50/p95/p99, all-endpoint p95, error rate, dropped iterations and achieved rate. Also each repeat separately and min/max across repeats, plus the host's CPUCreditBalance and CPUUtilization from CloudWatch over the run window. No best-run selection, and failed or saturated runs stay in. The Target-resume gate (sustained 100 requests/s for 10 minutes with p95 below 200 ms and errors below 1%) is judged per variant across all three repeats.

**Limits.** One home internet path (latency includes the WAN round trip to us-west-2) and one run day. The live service shares the host and its CPU credits. Burstable credits can run out during the uncached variant, which then runs throttled at baseline. That is recorded, not avoided. Load-machine background activity isn't controlled.

**Reproduce.**

```
# host (operator machine, once)
ssh <host> 'sudo install -m 755 /dev/stdin /opt/classmap-transit/bench-host.sh' < bench/remote/host.sh
ssh <host> /opt/classmap-transit/bench-host.sh prepare
# load machine (PowerShell)
bench\remote\run-load.ps1 -HostAddr <ip> -Key <key> -Out bench\raw\v2
python3 bench/summarize.py bench/raw/v2
```
