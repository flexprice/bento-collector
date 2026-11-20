import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

export const cn = (...v: ClassValue[]) => twMerge(clsx(v))

export interface Connection {
  id: string
  name?: string
  type: 'prometheus' | 'flexprice'
  url: string
  auth_type?: string
  username?: string
  secret_env?: string
  headers?: Record<string, string>
}

export type WindowSize = '1h' | '2h' | '3h' | '4h' | '6h' | '8h' | '12h' | '1d'
export type CustomerFrom = 'label' | 'static'

export interface Collector {
  id: string
  name?: string
  version?: number
  saved_at?: string
  saved_by?: string
  published?: Published
  source: { type: 'prometheus'; connection: string; query: string; mode: 'window' | 'usage_date' }
  trigger: { window: WindowSize; delay: string; completeness?: { query: string; min: number } }
  mapping: {
    meter_id?: string
    event_name: string
    value_property: string
    customer: { from: CustomerFrom; value?: string; label?: string }
    required_labels?: string[]
    property_labels?: Record<string, string>
    properties?: Record<string, string>
    timestamp: 'window_start' | 'window_end'
    event_source?: string
  }
  destination: string
}

export interface Meter {
  id: string
  name: string
  event_name: string
  aggregation: { type: string; field?: string; expression?: string }
  filters?: { key: string; values: string[] }[] | null
}
// meterValueProperty is the event property a meter reads, or '' when it reads none.
export const meterValueProperty = (m: Meter) => (m.aggregation.expression || m.aggregation.type === 'COUNT' ? '' : m.aggregation.field ?? '')

export interface Customer { id: string; external_id: string; name: string }

export interface Sample { labels: Record<string, string>; value: string }
export interface SampleResult { window: { start: string; end: string }; query: string; evaluated_at: string; rows: Sample[]; truncated: boolean }

export interface Published {
  version: number
  published_at: string
  published_by?: string
}

export interface FlexEvent {
  event_id: string
  event_name: string
  external_customer_id: string
  timestamp: string
  source: string
  properties: Record<string, string>
}

export interface PreviewResult {
  collector: string
  version: number
  window: { start: string; end: string }
  query: string
  evaluated_at: string
  completeness?: { query: string; min: number; values: number[]; ok: boolean; error?: string }
  rows: { row?: { labels?: Record<string, string>; metric?: Record<string, string>; value?: string | [number, string] }; event?: FlexEvent; error?: string }[]
  events: number
  rejected: number
  checks: { level: 'ok' | 'warning' | 'error'; message: string }[]
  ok: boolean
}


export async function api<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  const res = await fetch(path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new Error((data as { error?: string }).error ?? res.statusText)
  return data as T
}

// Windows mirror spec.WindowSizes (hours that divide a day); delays are the server defaults.
export const WINDOWS: { size: WindowSize; label: string; short: string; noun: string; seconds: number; delay: string }[] = [
  ...[1, 2, 3, 4, 6, 8, 12].map((h) => ({ size: `${h}h` as WindowSize, label: h === 1 ? '1 hour' : `${h} hours`, short: h === 1 ? '1 hour' : `${h} hours`, noun: h === 1 ? 'hour' : `${h}-hour window`, seconds: h * 3600, delay: '5m' })),
  { size: '1d', label: '24 hours (1 day)', short: '1 day', noun: 'day', seconds: 86400, delay: '15m' },
]
export const windowOf = (size: WindowSize) => WINDOWS.find((w) => w.size === size) ?? WINDOWS[WINDOWS.length - 1]
export const minutes = (d: string) => { const m = /^(?:(\d+)h)?(?:(\d+)m)?$/.exec(d); return m ? Number(m[1] ?? 0) * 60 + Number(m[2] ?? 0) : 0 }

// runsAt describes when Bento runs a published collector (spec.Trigger.Cron).
export function runsAt(t: Collector['trigger']): string {
  const w = windowOf(t.window), m = minutes(t.delay || w.delay), mm = String(m % 60).padStart(2, '0')
  const hours = w.seconds / 3600
  if (hours === 1) return `every hour at :${mm} UTC, for the previous hour`
  if (hours < 24) return `every ${hours} hours at :${mm} UTC (from 00:${mm}), for the previous ${w.noun}`
  return `daily at ${String(Math.floor(m / 60)).padStart(2, '0')}:${mm} UTC, for the previous day`
}

// lastClosed is the start of the newest window that closed at least delay ago (spec.LastClosed).
export function lastClosed(size: WindowSize, delay: string, now = Date.now()): Date {
  const s = windowOf(size).seconds
  const end = Math.floor((Math.floor(now / 1000) - minutes(delay) * 60) / s) * s
  return new Date((end - s) * 1000)
}

// byLabels reads the label names of the query's outermost by (…) clauses, so
// labels can be offered before a sample returns rows. Empty when there is none.
export function byLabels(query: string): string[] {
  const depth: number[] = []
  let d = 0
  for (const ch of query) { if (ch === ')') d--; depth.push(d); if (ch === '(') d++ }
  const found: { at: number; labels: string[] }[] = []
  for (const m of query.matchAll(/\bby\s*\(([^()]*)\)/g)) {
    found.push({ at: depth[m.index ?? 0] ?? 0, labels: m[1].split(',').map((s) => s.trim()).filter(Boolean) })
  }
  if (!found.length) return []
  const top = Math.min(...found.map((f) => f.at))
  return [...new Set(found.filter((f) => f.at === top).flatMap((f) => f.labels))]
}

// distinctValues collects each label's distinct values across sample rows, most frequent first.
export function distinctValues(rows: Sample[]): Record<string, string[]> {
  const counts: Record<string, Map<string, number>> = {}
  for (const r of rows) for (const [k, v] of Object.entries(r.labels)) {
    if (k === '__name__') continue
    const m = (counts[k] ??= new Map())
    m.set(v, (m.get(v) ?? 0) + 1)
  }
  return Object.fromEntries(Object.entries(counts).map(([k, m]) => [k, [...m.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).map((e) => e[0])]))
}
export const parseList = (s: string) => s.split(',').map((x) => x.trim()).filter(Boolean)

function sortKeys(v: unknown): unknown {
  if (Array.isArray(v)) return v.map(sortKeys)
  if (v && typeof v === 'object') return Object.fromEntries(Object.keys(v).sort().map((k) => [k, sortKeys((v as Record<string, unknown>)[k])]))
  return v
}
// spec is the collector as the API accepts it: without the server-set published field.
export function spec(c: Collector): Collector {
  const { published: _p, ...rest } = c
  return rest
}

export function canonical(c: Collector | null): string {
  if (!c) return ''
  const { version: _v, saved_at: _s, saved_by: _b, published: _p, ...rest } = c
  return JSON.stringify(sortKeys(rest), null, 2)
}

export function lineDiff(a: string, b: string): [' ' | '+' | '-', string][] {
  const x = a ? a.split('\n') : [], y = b ? b.split('\n') : []
  const m = x.length, n = y.length
  const dp = Array.from({ length: m + 1 }, () => new Array<number>(n + 1).fill(0))
  for (let i = m - 1; i >= 0; i--) for (let j = n - 1; j >= 0; j--) dp[i][j] = x[i] === y[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1])
  const out: [' ' | '+' | '-', string][] = []
  let i = 0, j = 0
  while (i < m && j < n) {
    if (x[i] === y[j]) { out.push([' ', x[i]]); i++; j++ } else if (dp[i + 1][j] >= dp[i][j + 1]) out.push(['-', x[i++]])
    else out.push(['+', y[j++]])
  }
  while (i < m) out.push(['-', x[i++]])
  while (j < n) out.push(['+', y[j++]])
  return out
}

export const relTime = (iso?: string) => {
  if (!iso) return ''
  const s = (Date.now() - new Date(iso).getTime()) / 1000
  if (s < 60) return 'just now'
  if (s < 3600) return `${Math.floor(s / 60)}m ago`
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`
  return new Date(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })
}
