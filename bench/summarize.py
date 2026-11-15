import json
import re
import sys
from pathlib import Path

VARIANTS = ["uncached", "indexed", "cached"]
ENDPOINTS = ["departures", "feed_status", "observations"]


def load(raw, rep, variant):
    path = raw / f"k6-{rep}-{variant}.json"
    if not path.exists():
        return None
    m = json.loads(path.read_text())["metrics"]
    trend = lambda key: m.get(key, {})
    plan = (raw / f"plan-{rep}-{variant}.txt").read_text()
    scan = re.findall(r"(Index Scan using \w+|Bitmap Index Scan on \w+|Seq Scan on \w+|Parallel Seq Scan on \w+)", plan)
    exec_ms = re.search(r"Execution Time: ([\d.]+) ms", plan)
    row = {
        "rep": rep,
        "variant": variant,
        "requests": m["http_reqs"]["count"],
        "achieved_rps": m["http_reqs"]["rate"],
        "error_rate": m["http_req_failed"]["value"],
        "dropped": m.get("dropped_iterations", {}).get("count", 0),
        "all_p95": trend("http_req_duration")["p(95)"],
        "plan_scan": sorted(set(scan)),
        "plan_ms": float(exec_ms.group(1)) if exec_ms else None,
        "aborted": "k6 exit" in (raw / f"k6-{rep}-{variant}.txt").read_text(),
    }
    for e in ENDPOINTS:
        t = trend(f"http_req_duration{{endpoint:{e}}}")
        row[e] = {k: t.get(k) for k in ["med", "p(95)", "p(99)", "max"]}
    return row


def fmt(x):
    return "—" if x is None else (f"{x:.2f}" if x < 100 else f"{x:.0f}")


def main():
    raw = Path(sys.argv[1] if len(sys.argv) > 1 else "bench/raw/v1")
    rows = [r for rep in range(3) for v in VARIANTS if (r := load(raw, rep, v))]
    (raw / "summary.json").write_text(json.dumps(rows, indent=2) + "\n")
    print("| rep | variant | departures p50 / p95 / p99 (ms) | all p95 (ms) | errors | dropped | achieved rps | plan |")
    print("|---|---|---|---|---|---|---|---|")
    for r in rows:
        d = r["departures"]
        print(f"| {r['rep']} | {r['variant']} | {fmt(d['med'])} / {fmt(d['p(95)'])} / {fmt(d['p(99)'])} | {fmt(r['all_p95'])} | "
              f"{r['error_rate']:.2%} | {r['dropped']} | {r['achieved_rps']:.1f} | {', '.join(r['plan_scan'])} ({fmt(r['plan_ms'])} ms) |")
    print()
    print("| variant | departures p95 min–max (ms) | departures p99 min–max (ms) | errors max | dropped total |")
    print("|---|---|---|---|---|")
    for v in VARIANTS:
        rs = [r for r in rows if r["variant"] == v]
        if not rs:
            continue
        p95 = [r["departures"]["p(95)"] for r in rs]
        p99 = [r["departures"]["p(99)"] for r in rs]
        print(f"| {v} ({len(rs)} runs) | {fmt(min(p95))}–{fmt(max(p95))} | {fmt(min(p99))}–{fmt(max(p99))} | "
              f"{max(r['error_rate'] for r in rs):.2%} | {sum(r['dropped'] for r in rs)} |")


if __name__ == "__main__":
    main()
