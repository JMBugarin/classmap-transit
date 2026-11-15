# Controlled local benchmark protocol v1

Frozen before the full run, in the commit that adds this file. Results come in later commits and don't change this file.

**Question.** For `/v1/departures` on one published generation of 1,000,000 observations, how do three variants compare: no stop index and no response cache (`uncached`), a stop index (`indexed`), and a stop index plus the 10 s in-process response cache (`cached`)?

**Data.** `transit bench-seed` with BENCH_ROWS=1000000 and BENCH_SEED=20261115 builds 760 synthetic stops (`S0000`..`S0759`) with Zipf(s=1.1) popularity, 25,000 synthetic trips of 40 observations each, and predicted times uniform over the 24 h after seeding. It only runs against a database named `*_bench`. No real METRO data, no real users.

**Load.** k6 `constant-arrival-rate` at 100 requests/s for 600 s, with 50 preallocated VUs and a cap of 400. The mix is 70% departures (Zipf stop choice, limit 20), 20% feed-status and 10% observations (limit 20). Each request is chosen from a fixed hash of iteration and VU, so the sequence is the same for every variant. A 30 s warmup at the same rate runs before each measured run and is kept but not reported as a result.

**Order.** Three repeats with the variant order rotated: (uncached, indexed, cached), (indexed, cached, uncached), (cached, uncached, indexed). The API restarts for every run. `ANALYZE observations` runs after each index change. Before each run, a query plan (`EXPLAIN ANALYZE, BUFFERS`) for one hot stop is saved.

**Freshness.** A heartbeat sets `feed_state.source_time` to now every 15 s, so responses stay on the fresh-prediction path instead of the stale/scheduled fallback. That's one small UPDATE every 15 s during each run, and it's disclosed here.

**Reported.** For every run, departures p50/p95/p99, all-endpoint p95, error rate, dropped iterations and achieved rate. Also each repeat separately plus min/max across repeats. No best-run selection, and failed or saturated runs stay in.

**Environment and limits.** API (native, WORKERS=1, 8 pool connections, 5 s statement timeout), PostgreSQL 17.12 (Docker in a 4-vCPU / 6 GB Colima VM) and k6 all share one Apple M5 Pro laptop. This misses the original acceptance gate of a separate load-generator host, so results are exploratory and same-machine. No cloud, no network hop, no burst credits. Laptop background load isn't controlled.

**Reproduce.**

```
createdb transit_bench   # on any PostgreSQL 17
BENCH_DATABASE_URL=postgres://.../transit_bench bench/run.sh
```
