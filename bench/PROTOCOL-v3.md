# In-region benchmark protocol v3

Frozen before the run, in the commit that adds this file. Results come in later commits and don't change this file. v2 (`PROTOCOL-v2.md`) sent the load over a home internet connection, so its 36 ms departures p95 was mostly the network path: the query plan ran in about 0.3 ms on the host. v3 keeps v2's host, image, data, k6 script and load, and moves k6 to an EC2 instance in the same Availability Zone, so the measured latency is the service's own.

**Question.** For `/v1/departures` on one published generation of 1,000,000 observations, what latency does the deployed host give a client in its own Availability Zone, with the stop index (`indexed`) and with the index plus the 10 s in-process response cache (`cached`)?

**Not repeated: `uncached`.** v1 and v2 both showed that without the stop index the host can't serve 100 requests/s: in every v2 run 70% of requests hit the 5 s statement timeout, and the uncached runs drained the host's CPU credits to 0. The live service now serves the public web map from the same host, so v3 leaves it out.

**Data.** Same as v2: `transit bench-seed` with BENCH_ROWS=1000000 and BENCH_SEED=20261115 into a separate `transit_bench` database in the deployed PostgreSQL container. Synthetic stops and trips only. The live `transit` database isn't touched. `transit_bench` is dropped after the run.

**Server.** Same as v2: EC2 t4g.small `i-061380374c32f3c5b` (us-west-2d, 2 vCPU Graviton, 2 GiB, standard CPU credits), PostgreSQL 17.12, bench image `classmap-transit:dffd820`, and `bench/remote/host.sh` unchanged (installed copy checked by sha256). The bench API container runs with `WORKERS=1`, `SKIP_MIGRATE=1` and the default 5 s statement timeout on port 18080. The live API (`c1d2ea0`), poller and Caddy ingress keep running beside it. Port 18080 and SSH are opened on the host only to the load instance's security group, for the duration of the run, and closed afterwards. The load instance reaches the host's private address, so no bench traffic leaves the VPC. It controls the host with a temporary key restricted to `bench-host.sh`, removed after the run.

**Load instance.** One c7i-flex.large (2 vCPU, 4 GiB, not burstable) in the host's subnet in us-west-2d, Amazon Linux 2023, k6 2.4.0 (the version v2 used, checksum verified) running the unchanged `bench/k6.js` with `BASE_URL=http://<host private address>:18080`. It runs `bench/remote/run-load.sh` from a clone of this repository at the frozen commit, and is terminated after the run. Before the first warmup it records 200 sequential requests to `/readyz` (connect and total time) as the network baseline.

**Load.** Unchanged from v2: k6 `constant-arrival-rate` at 100 requests/s for 600 s (50 preallocated VUs, cap 400), a 70/20/10 mix of departures, feed-status and observations, a deterministic request sequence (SEED 20261115), and a 30 s warmup before each measured run that is kept but not reported.

**Order.** Three repeats with alternating variant order: (indexed, cached), (cached, indexed), (indexed, cached). The bench API restarts for every run. `ANALYZE observations` runs after each index change, and the query plan for one hot stop is saved before each run.

**Freshness.** Unchanged: a heartbeat on the host sets the bench database's `feed_state.source_time` to now every 15 s.

**Reported.** For every run: departures p50/p95/p99, all-endpoint p95, error rate, dropped iterations and achieved rate. Also each repeat separately and min/max across repeats, the network baseline (median and p95 connect time), and the host's CPUCreditBalance and CPUUtilization from CloudWatch over the run window. No best-run selection; failed runs stay in. The v2 gate (100 requests/s for 10 minutes, p95 under 200 ms, errors under 1%, in every repeat) is judged per variant. Any resume figure uses the worst indexed departures p95 of the three repeats.

**Limits.** One Availability Zone, one day, one host shared with the live service. Synthetic data, not real traffic. The bench port bypasses Caddy and TLS, as in v2, so real web-map clients also pay TLS and their own internet path. The load instance shares the subnet with the host, so this is a best case for network distance, not what a user sees.

**Cost.** One free-tier-eligible c7i-flex.large for about two hours, its public IPv4 address and 8 GB gp3 volume. The account's Free plan rejected a non-eligible type.

**Reproduce.**

```
# operator machine: host side, once (through the load instance as a jump host)
ssh -J ec2-user@<load> ec2-user@<host> /opt/classmap-transit/bench-host.sh prepare
# load instance
git clone https://github.com/JMBugarin/classmap-transit && cd classmap-transit && git checkout <frozen commit>
bench/remote/run-load.sh <host private address> ~/.ssh/bench_key bench/raw/v3
python3 bench/summarize.py bench/raw/v3
```
