import { afterEach, expect, test, vi } from 'vitest'
import { displayState, getDepartures, getOps, type Status } from './client'

const status: Status = { generation: 1, source_time: 1, fetched_at: null, processed_at: null, freshness: 'stale' }
const respond = (body: unknown, ok = true, code = 200) => vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok, status: code, json: async () => body }))

afterEach(() => vi.unstubAllGlobals())

test('rejects failed and malformed departures', async () => {
  respond(null, false, 503)
  await expect(getDepartures('', 'a')).rejects.toThrow('503')
  respond({ departures: [] })
  await expect(getDepartures('', 'a')).rejects.toThrow('Invalid')
  respond({ status, departures: [{ trip: 'a', stop: 'b', time: 1, kind: 'cancelled' }] })
  await expect(getDepartures('', 'a')).rejects.toThrow('Invalid')
})

test('encodes the stop id', async () => {
  respond({ status, departures: [] })
  await getDepartures('/api', 'a&b')
  expect(vi.mocked(fetch).mock.calls[0][0]).toBe('/api/v1/departures?stop=a%26b&limit=20')
})

test('labels scheduled fallback without promoting source freshness', () => {
  expect(displayState({ status, departures: [{ trip: 'a', stop: 'b', time: 2, event_time: null, kind: 'scheduled' }] })).toBe('scheduled')
  expect(displayState({ status, departures: [] })).toBe('stale')
})

test('validates ops counts', async () => {
  respond({ status, jobs: { done: 2 }, outcomes: { published: 1 }, schedule: null })
  await expect(getOps('')).resolves.toMatchObject({ jobs: { done: 2 } })
  respond({ status, jobs: { done: -1 }, outcomes: {}, schedule: null })
  await expect(getOps('')).rejects.toThrow('Invalid')
})
