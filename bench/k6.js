import http from 'k6/http'
import { check } from 'k6'

const BASE = __ENV.BASE_URL || 'http://127.0.0.1:18080'
const RATE = Number(__ENV.RATE || 100)
const DURATION = __ENV.DURATION || '600s'
const STOPS = 760

export const options = {
  discardResponseBodies: true,
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
  scenarios: {
    mix: { executor: 'constant-arrival-rate', rate: RATE, timeUnit: '1s', duration: DURATION, preAllocatedVUs: 50, maxVUs: 400 },
  },
  thresholds: {
    'http_req_duration{endpoint:departures}': ['p(95)>=0'],
    'http_req_duration{endpoint:feed_status}': ['p(95)>=0'],
    'http_req_duration{endpoint:observations}': ['p(95)>=0'],
  },
}

// Deterministic per-iteration choice: same request sequence for every variant and repeat.
function mix32(x) {
  x = Math.imul(x ^ (x >>> 16), 0x7feb352d)
  x = Math.imul(x ^ (x >>> 15), 0x846ca68b)
  return (x ^ (x >>> 16)) >>> 0
}
const zipfCdf = (() => {
  const w = Array.from({ length: STOPS }, (_, k) => 1 / Math.pow(k + 1, 1.1))
  const total = w.reduce((a, b) => a + b, 0)
  let acc = 0
  return w.map(v => (acc += v / total))
})()
function stopFor(u) {
  let lo = 0
  let hi = STOPS - 1
  while (lo < hi) {
    const mid = (lo + hi) >> 1
    if (zipfCdf[mid] < u) lo = mid + 1
    else hi = mid
  }
  return 'S' + String(lo).padStart(4, '0')
}

export default function () {
  const i = Number(__ENV.SEED || 20261115) + __ITER + 1_000_003 * __VU
  const h = mix32(i)
  const pick = h % 100
  let res
  if (pick < 70) {
    res = http.get(`${BASE}/v1/departures?stop=${stopFor(mix32(h) / 4294967296)}&limit=20`, { tags: { endpoint: 'departures' } })
  } else if (pick < 90) {
    res = http.get(`${BASE}/v1/feed-status`, { tags: { endpoint: 'feed_status' } })
  } else {
    res = http.get(`${BASE}/v1/observations?limit=20`, { tags: { endpoint: 'observations' } })
  }
  check(res, { 'status 200': r => r.status === 200 })
}
