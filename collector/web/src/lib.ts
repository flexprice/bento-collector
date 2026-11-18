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
  secret_ref?: string
  headers?: Record<string, string>
  updated_at?: string
  has_secret?: boolean
  secret?: string
  managed?: boolean
}

export interface Collector {
  id: string
  name?: string
  version?: number
  saved_at?: string
  saved_by?: string
  source: { type: 'prometheus'; connection: string; query: string; mode: 'window' | 'usage_date' }
  trigger: { schedule: string; window: string; completeness?: { query: string; min: number } }
  mapping: {
    event_name: string
    customer: { from: 'label' | 'static'; value?: string; label?: string; lookup?: Record<string, string>; skip?: string[] }
    unit?: string
    required_labels?: string[]
    drop_labels?: string[]
    properties?: Record<string, string>
    timestamp: 'window_start' | 'window_end'
    event_source?: string
  }
  destination: string
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
  rows: { series?: { metric: Record<string, string>; value: [number, string] }; event?: FlexEvent; error?: string }[]
  events: number
  rejected: number
  skipped: number
  checks: { level: 'ok' | 'warning' | 'error'; message: string }[]
  ok: boolean
}

export class Unauthorized extends Error {}

let token = localStorage.getItem('collectorToken') ?? ''
export const setToken = (t: string) => {
  token = t
  localStorage.setItem('collectorToken', t)
}

export async function api<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  if (token) headers.Authorization = 'Bearer ' + token
  const res = await fetch(path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) })
  const data = await res.json().catch(() => ({}))
  if (res.status === 401) throw new Unauthorized((data as { error?: string }).error ?? 'Unauthorized')
  if (!res.ok) throw new Error((data as { error?: string }).error ?? res.statusText)
  return data as T
}

export const yesterday = () => new Date(Date.now() - 86400000).toISOString().slice(0, 10)
export const parseList = (s: string) => s.split(',').map((x) => x.trim()).filter(Boolean)

function sortKeys(v: unknown): unknown {
  if (Array.isArray(v)) return v.map(sortKeys)
  if (v && typeof v === 'object') return Object.fromEntries(Object.keys(v).sort().map((k) => [k, sortKeys((v as Record<string, unknown>)[k])]))
  return v
}
export function canonical(c: Collector | null): string {
  if (!c) return ''
  const { version: _v, saved_at: _s, saved_by: _b, ...rest } = c
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
