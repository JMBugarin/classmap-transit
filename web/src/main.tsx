import { useEffect, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { displayState, getDepartures, getOps, type Departures, type Ops } from './client'
import './style.css'

const BASE = '/api'
const when = (s: number | null) => (s ? new Date(s * 1000).toISOString() : 'Unknown')

function usePoll<T>(load: (signal: AbortSignal) => Promise<T>, key: string, every: number) {
  const [value, setValue] = useState<T | null>(null)
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    const ctl = new AbortController()
    const tick = async () => {
      try {
        const v = await load(ctl.signal)
        if (!ctl.signal.aborted) { setValue(v); setFailed(false) }
      } catch {
        if (!ctl.signal.aborted) { setValue(null); setFailed(true) }
      }
    }
    void tick()
    const id = setInterval(tick, every)
    return () => { ctl.abort(); clearInterval(id) }
  }, [key, every])
  return { value, failed }
}

function Counts({ label, counts }: { label: string; counts: Record<string, number> }) {
  const entries = Object.entries(counts).sort(([a], [b]) => a.localeCompare(b))
  return (
    <section aria-label={label}>
      <h2>{label}</h2>
      {entries.length ? <ul>{entries.map(([k, n]) => <li key={k}>{k}: {n}</li>)}</ul> : <p>None</p>}
    </section>
  )
}

function App() {
  const [stop, setStop] = useState('1660')
  const departures = usePoll<Departures>(signal => getDepartures(BASE, stop, signal), stop, 10000)
  const ops = usePoll<Ops>(signal => getOps(BASE, signal), 'ops', 10000)
  const d = departures.value
  return (
    <main>
      <h1>Classmap Transit</h1>
      <p>Operator status · local development</p>
      <label>Stop ID <input value={stop} onChange={e => setStop(e.target.value)} /></label>
      <p role="status">{departures.failed ? 'Transit unavailable' : d ? displayState(d) : 'Loading'}</p>
      {d && (
        <>
          <dl>
            <dt>Generation</dt><dd>{d.status.generation ?? 'None'}</dd>
            <dt>Source time</dt><dd>{when(d.status.source_time)}</dd>
            <dt>Fetched</dt><dd>{d.status.fetched_at ?? 'Unknown'}</dd>
            <dt>Processed</dt><dd>{d.status.processed_at ?? 'Unknown'}</dd>
          </dl>
          <ul aria-label="Departures">
            {d.departures.map(x => <li key={`${x.trip}-${x.time}`}>{x.trip} · {new Date(x.time * 1000).toLocaleTimeString()} · {x.kind}</li>)}
          </ul>
        </>
      )}
      {ops.failed && <p>Operator summary unavailable</p>}
      {ops.value && (
        <div className="ops">
          <Counts label="Jobs" counts={ops.value.jobs} />
          <Counts label="Outcomes" counts={ops.value.outcomes} />
          <p>Schedule {ops.value.schedule ? ops.value.schedule.slice(0, 12) : 'not imported'}</p>
        </div>
      )}
      <p>Data: Santa Cruz METRO. No endorsement. Missing predictions do not mean cancellation.</p>
    </main>
  )
}

createRoot(document.getElementById('root')!).render(<App />)
