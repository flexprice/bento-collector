import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { CheckCircle2Icon, CheckIcon, ChevronRightIcon, CircleStopIcon, Trash2Icon, HistoryIcon, PlayIcon, PlusIcon, RocketIcon, RotateCcwIcon, SaveIcon, TriangleAlertIcon, XIcon } from 'lucide-react'
import { useApp } from '@/App'
import {
  api, byLabels, canonical, cn, distinctValues, lastClosed, lineDiff, meterValueProperty, runsAt, spec, windowOf, WINDOWS,
  type Collector, type Customer, type Meter, type PreviewResult, type SampleResult, type WindowSize,
} from '@/lib'
import { Stepper, StepperIndicator, StepperItem, StepperNav, StepperSeparator, StepperTitle, StepperTrigger } from '@/components/reui/stepper'
import { Alert, Badge, Button, Code, Combobox, Empty, Field, Input, PageHeader, Section, Select, Spinner, Switch, Table, Td, Textarea, Tr } from '@/components/ui'

type Step = 'query' | 'map' | 'schedule' | 'preview'
const STEPS: [Step, string][] = [['query', 'Query'], ['map', 'Map to events'], ['schedule', 'Schedule'], ['preview', 'Preview & save']]

function blank(connections: ReturnType<typeof useApp>['connections']): Collector {
  return {
    id: '', name: '',
    source: { type: 'prometheus', connection: connections.find((c) => c.type === 'prometheus')?.id ?? '', query: '', mode: 'window' },
    trigger: { window: '1d', delay: windowOf('1d').delay },
    mapping: { event_name: '', value_property: 'value', customer: { from: 'label', label: '' }, properties: {}, timestamp: 'window_start' },
    destination: connections.find((c) => c.type === 'flexprice')?.id ?? '',
  }
}

// withDefaults fills fields older saved versions may lack, as the server's Normalize does.
function withDefaults(c: Collector): Collector {
  const n = structuredClone(c)
  n.trigger.window ||= '1d'
  // A window this editor no longer offers moves to 1 hour; QueryStep says so.
  if (!WINDOWS.some((w) => w.size === n.trigger.window)) { n.trigger.window = '1h'; n.trigger.delay = windowOf('1h').delay }
  n.trigger.delay ||= windowOf(n.trigger.window).delay
  n.mapping.value_property ||= 'value'
  n.mapping.timestamp ||= 'window_start'
  return n
}

const uniq = (xs: string[]) => [...new Set(xs.filter(Boolean))]
type Range = { from: string; to: string; day: string }
// fmtNum trims float noise for display only; events carry the raw value.
const fmtNum = (v: string) => { const n = Number(v); return Number.isFinite(n) ? String(Math.round(n * 1000) / 1000) : v }

export function CollectorEditor({ id }: { id: string | null }) {
  const { connections, toast, reload, go } = useApp()
  const [saved, setSaved] = useState<Collector | null>(null)
  const [draft, setDraft] = useState<Collector | null>(id ? null : blank(connections))
  const [view, setView] = useState<'configure' | 'versions' | 'config'>('configure')
  const [step, setStep] = useState<Step>('query')
  const [sample, setSample] = useState<SampleResult | null>(null)
  const [range, setRange] = useState<Range>({ from: '', to: '', day: '' })
  const [preview, setPreview] = useState<PreviewResult | null>(null)
  const [previewKey, setPreviewKey] = useState('')
  const [previewWin, setPreviewWin] = useState('')
  const [meters, setMeters] = useState<Meter[] | null>(null)
  const [metersError, setMetersError] = useState('')

  useEffect(() => {
    if (!id) return
    api<Collector>('GET', `/api/collectors/${id}`).then((c) => { setSaved(c); setDraft(withDefaults(c)) }).catch((e) => toast(e.message, true))
  }, [id, toast])

  const dest = draft?.destination ?? ''
  useEffect(() => {
    if (!dest) return
    setMeters(null); setMetersError('')
    api<Meter[] | null>('GET', `/api/connections/${dest}/meters`).then((r) => setMeters(r ?? [])).catch((e) => { setMeters([]); setMetersError(e.message) })
  }, [dest])

  const [sampling, setSampling] = useState(false)
  const [autoKey, setAutoKey] = useState('')
  // Labels for the Map step: from the sample's rows, else from the query's by (…).
  const query = draft?.source.query ?? ''
  const distinct = useMemo(() => {
    const seen = distinctValues(sample?.rows ?? [])
    if (Object.keys(seen).length) return seen
    return Object.fromEntries(byLabels(query).map((l) => [l, [] as string[]]))
  }, [sample, query])
  // Opening Map without a sample runs one in the background, once per query, window and mode.
  useEffect(() => {
    if (step !== 'map' || sample || !draft?.source.query.trim() || !draft.source.connection) return
    const key = JSON.stringify([draft.source, draft.trigger.window])
    if (key === autoKey) return
    setAutoKey(key); setSampling(true)
    api<SampleResult>('POST', '/api/sample', { source: draft.source, trigger: { window: draft.trigger.window, delay: draft.trigger.delay } })
      .then(setSample).catch(() => {}).finally(() => setSampling(false))
  }, [step, sample, draft?.source, draft?.trigger.window]) // eslint-disable-line react-hooks/exhaustive-deps

  if (!draft) return <div className="text-muted-foreground flex items-center gap-2 p-8 text-sm"><Spinner /> Loading…</div>
  const fresh = !!preview && previewKey === canonical(draft)
  const base = saved ? withDefaults(saved) : null
  const dirty = canonical(base) !== canonical(draft)
  const update = (fn: (d: Collector) => void) => setDraft((d) => { const n = structuredClone(d!); fn(n); return n })
  const meter = meters?.find((m) => m.id === draft.mapping.meter_id) ?? meters?.find((m) => m.event_name === draft.mapping.event_name)

  const runPreview = async () => {
    const r = await api<PreviewResult>('POST', '/api/preview', { collector: spec(draft), window: previewWin })
    setPreview(r); setPreviewKey(canonical(draft))
  }
  const save = async () => {
    const { version: _v, saved_at: _s, saved_by: _b, ...body } = spec(draft)
    const s = await api<Collector>('PUT', `/api/collectors/${encodeURIComponent(draft.id)}`, body)
    toast(`Saved ${s.id} as v${s.version}. Publish it to run it.`)
    await reload()
    if (!id) { go(`#/collectors/${s.id}`); return }
    s.published = saved?.published
    setSaved(s); setDraft(withDefaults(s)); setPreviewKey(canonical(withDefaults(s)))
  }
  const refresh = async () => {
    const c = await api<Collector>('GET', `/api/collectors/${saved!.id}`)
    setSaved(c); await reload()
  }
  const publish = async (version: number) => {
    try {
      await api('POST', `/api/collectors/${saved!.id}/publish`, { version })
      toast(`Published v${version}. Bento runs it ${runsAt(saved!.trigger)}.`)
      await refresh()
    } catch (e) { toast((e as Error).message, true) }
  }
  const unpublish = async () => {
    if (!confirm(`Stop running ${saved!.id}? Versions are kept.`)) return
    try {
      await api('DELETE', `/api/collectors/${saved!.id}/publish`)
      toast(`Unpublished ${saved!.id}`)
      await refresh()
    } catch (e) { toast((e as Error).message, true) }
  }
  const remove = async () => {
    if (!confirm(`Delete ${saved!.id}? Bento stops running it and every version is removed.`)) return
    try {
      await api('DELETE', `/api/collectors/${saved!.id}`)
      toast(`Deleted ${saved!.id}`)
      await reload()
      go('#/collectors')
    } catch (e) { toast((e as Error).message, true) }
  }
  const published = saved?.published
  const idx = STEPS.findIndex((s) => s[0] === step)

  return (
    <div>
      <PageHeader
        breadcrumb={<a href="#/collectors" className="text-muted-foreground hover:text-foreground flex items-center gap-1 text-xs">Collectors <ChevronRightIcon className="size-3" /> {saved ? saved.id : 'New'}</a>}
        title={<span className="flex items-center gap-2">{draft.name || draft.id || 'New collector'} {saved ? <Badge tone="info">v{saved.version}</Badge> : null}{saved ? (published ? <Badge tone="success" dot>Published v{published.version}</Badge> : <Badge>Not published</Badge>) : null}{dirty && saved ? <Badge tone="warning" dot>Unsaved</Badge> : null}</span>}
        meta={published ? `Runs ${runsAt(saved!.trigger)}` : undefined}
        actions={saved ? (
          <div className="flex items-center gap-2">
            {published?.version !== saved.version ? <Button size="sm" onClick={() => publish(saved.version!)}><RocketIcon aria-hidden /> Publish v{saved.version}</Button> : null}
            {published ? <Button size="sm" variant="outline" onClick={unpublish}><CircleStopIcon aria-hidden /> Unpublish</Button> : null}
            <Button size="sm" variant="ghost" onClick={remove} aria-label="Delete collector" title="Delete collector"><Trash2Icon aria-hidden /></Button>
            <div className="bg-muted flex rounded-lg p-0.5">
              {(['configure', 'versions', 'config'] as const).map((v) => (
                <button key={v} onClick={() => setView(v)} className={cn('h-7 rounded-md px-3 text-[13px] capitalize', view === v ? 'bg-background font-medium shadow-xs' : 'text-muted-foreground')}>
                  {v === 'config' ? 'Bento config' : v}
                </button>
              ))}
            </div>
          </div>
        ) : null}
      />
      <div className="px-8 pb-10">
        {view === 'versions' && saved ? <Versions id={saved.id} published={published?.version} onPublish={publish} onEdit={(c) => { setDraft({ ...withDefaults(c), version: saved.version, published }); setView('configure'); setStep('preview'); toast(`Loaded v${c.version}. Preview it, then save it as a new version.`) }} /> : null}
        {view === 'config' ? <RenderedConfig draft={draft} /> : null}
        {view === 'configure' ? (
          <div className="grid max-w-6xl gap-6 lg:grid-cols-[170px_minmax(0,1fr)]">
            <Stepper value={idx + 1} onValueChange={(v) => setStep(STEPS[v - 1][0])} orientation="vertical" indicators={{ completed: <CheckIcon className="size-3.5" /> }}>
              <StepperNav>
                {STEPS.map(([k, label], i) => (
                  <StepperItem key={k} step={i + 1} className="relative items-start not-last:flex-1">
                    <StepperTrigger className="items-center gap-2.5 rounded-lg pb-6 text-left">
                      <StepperIndicator>{i + 1}</StepperIndicator>
                      <StepperTitle>{label}</StepperTitle>
                    </StepperTrigger>
                    {i < STEPS.length - 1 ? <StepperSeparator className="group-data-[state=completed]/step:bg-primary absolute inset-y-0 top-7 left-3 -order-1 m-0 -translate-x-1/2 group-data-[orientation=vertical]/stepper-nav:h-[calc(100%-2rem)]" /> : null}
                  </StepperItem>
                ))}
              </StepperNav>
            </Stepper>
            <div className="flex min-w-0 flex-col gap-4">
              {step === 'query' && saved && !WINDOWS.some((w) => w.size === saved.trigger.window) ? <Alert level="warning">This collector was saved with a {saved.trigger.window} window, which is no longer supported. It now uses 1 hour; pick a window and save a new version.</Alert> : null}
              {step === 'query' ? <QueryStep d={draft} update={update} isNew={!saved} sample={sample} setSample={setSample} range={range} setRange={setRange} /> : null}
              {step === 'map' ? <MapStep d={draft} update={update} meters={meters} metersError={metersError} meter={meter} distinct={distinct} sample={sample} sampling={sampling} /> : null}
              {step === 'schedule' ? <ScheduleStep d={draft} update={update} /> : null}
              {step === 'preview' ? <PreviewStep d={draft} saved={base} win={previewWin} setWin={setPreviewWin} run={runPreview} preview={preview} fresh={fresh} onSave={save} /> : null}
              <div className="flex justify-between">
                <Button variant="outline" disabled={idx === 0} onClick={() => setStep(STEPS[idx - 1][0])}>Back</Button>
                {idx < STEPS.length - 1 ? <Button variant="outline" onClick={() => setStep(STEPS[idx + 1][0])}>Continue <ChevronRightIcon aria-hidden /></Button> : null}
              </div>
            </div>
          </div>
        ) : null}
      </div>
    </div>
  )
}

type StepProps = { d: Collector; update: (fn: (d: Collector) => void) => void }

// More folds options most collectors leave alone.
function More({ title, children }: { title: string; children: ReactNode }) {
  return (
    <details className="bg-background group rounded-xl border">
      <summary className="text-muted-foreground hover:text-foreground flex cursor-pointer list-none items-center gap-1.5 px-5 py-3 text-[13px] select-none">
        <ChevronRightIcon className="size-3.5 transition-transform group-open:rotate-90" aria-hidden /> {title}
      </summary>
      <div className="flex flex-col gap-4 border-t p-5">{children}</div>
    </details>
  )
}

// toInput/fromInput convert between a UTC instant and a datetime-local value read as UTC.
const toInput = (d: Date) => d.toISOString().slice(0, 16)
// snap rounds a datetime-local value down to the window boundary.
function snap(v: string, size: WindowSize): string {
  if (!v) return v
  const n = windowOf(size).seconds / 3600
  const h = n >= 24 ? 0 : Math.floor(Number(v.slice(11, 13)) / n) * n
  return `${v.slice(0, 10)}T${String(h).padStart(2, '0')}:00`
}

// DateTime is one date-and-time field in UTC, snapped to whole hours or days.
function DateTime({ label, value, onChange, size, max }: { label: string; value: string; onChange: (v: string) => void; size: WindowSize; max?: string }) {
  return (
    <label className="flex items-center gap-1.5 text-xs">
      <span className="text-muted-foreground">{label}</span>
      <Input type="datetime-local" className="w-60" step={windowOf(size).seconds} value={value} max={max} aria-label={`${label} (UTC)`}
        onChange={(e) => onChange(snap(e.target.value, size))} />
    </label>
  )
}

// DayPicker picks one UTC day, for dated snapshots.
function DayPicker({ value, onChange, max }: { value: string; onChange: (v: string) => void; max: string }) {
  return (
    <label className="flex items-center gap-1.5 text-xs">
      <span className="text-muted-foreground">Day</span>
      <Input type="date" className="w-40" value={value} max={max} aria-label="Day (UTC)" onChange={(e) => onChange(e.target.value)} />
    </label>
  )
}

// WindowPicker picks one closed window by its start. '' means the newest closed one.
function WindowPicker({ size, delay, value, onChange, snapshot }: { size: WindowSize; delay: string; value: string; onChange: (v: string) => void; snapshot?: boolean }) {
  const newest = lastClosed(size, delay)
  return (
    <div className="flex items-center gap-1.5">
      {snapshot
        ? <DayPicker value={(value || toInput(newest)).slice(0, 10)} max={toInput(newest).slice(0, 10)} onChange={onChange} />
        : <DateTime label="Window" size={size} value={value || toInput(newest)} max={toInput(newest)} onChange={onChange} />}
      <span className="text-muted-foreground text-xs">UTC</span>
    </div>
  )
}

function QueryStep({ d, update, isNew, sample, setSample, range, setRange }: StepProps & { isNew: boolean; sample: SampleResult | null; setSample: (s: SampleResult | null) => void; range: Range; setRange: (r: Range) => void }) {
  const { connections } = useApp()
  const proms = connections.filter((c) => c.type === 'prometheus')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const snapshot = d.source.mode === 'usage_date'
  const run = async () => {
    setBusy(true); setError('')
    const body = snapshot
      ? { from: day, to: toInput(new Date(Date.parse(`${day}T00:00:00Z`) + 86400000)) }
      : { from, to }
    setSample(null)
    try { setSample(await api<SampleResult>('POST', '/api/sample', { source: d.source, trigger: { window: d.trigger.window, delay: d.trigger.delay }, ...body })) } catch (e) { setError((e as Error).message) } finally { setBusy(false) }
  }
  const newest = lastClosed(d.trigger.window, d.trigger.delay)
  const end = new Date(newest.getTime() + windowOf(d.trigger.window).seconds * 1000)
  const to = range.to || toInput(end)
  const from = range.from || toInput(new Date(end.getTime() - 7 * 86400000))
  const day = range.day || toInput(newest).slice(0, 10)
  const fixed = snapshot || !d.source.query.includes('__WINDOW__')
  const setMode = (mode: Collector['source']['mode']) => update((n) => {
    n.source.mode = mode
    if (mode === 'usage_date') { n.trigger.window = '1d'; n.trigger.delay = windowOf('1d').delay }
  })
  const labels = uniq((sample?.rows ?? []).flatMap((r) => Object.keys(r.labels))).filter((l) => l !== '__name__').sort()
  return (
    <>
      <Section title="Query">
        <div className="grid grid-cols-2 gap-4">
          <Field label="ID"><Input value={d.id} disabled={!isNew} placeholder="api-requests" onChange={(e) => update((n) => { n.id = e.target.value })} /></Field>
          <Field label="Name"><Input value={d.name ?? ''} placeholder="API requests" onChange={(e) => update((n) => { n.name = e.target.value })} /></Field>
          <Field label="Prometheus"><Select value={d.source.connection} onChange={(e) => update((n) => { n.source.connection = e.target.value })} options={[['', 'Select…'], ...proms.map((c) => [c.id, c.id] as [string, string])]} /></Field>
          <Field label="Mode" group hint={snapshot ? 'The metric holds a total per day in a usage_date label. One day per run.' : 'The query sums usage over each window.'}>
            <div className="bg-muted flex w-fit rounded-lg p-0.5" role="radiogroup" aria-label="Mode">
              {([['window', 'Range'], ['usage_date', 'Dated snapshot']] as const).map(([k, l]) => (
                <button key={k} type="button" role="radio" aria-checked={d.source.mode === k} onClick={() => setMode(k)}
                  className={cn('h-7 rounded-md px-3 text-[13px]', d.source.mode === k ? 'bg-background font-medium shadow-xs' : 'text-muted-foreground')}>{l}</button>
              ))}
            </div>
          </Field>
          {!snapshot ? (
            <Field label="Window">
              <Select value={d.trigger.window} onChange={(e) => update((n) => { const w = e.target.value as WindowSize; n.trigger.window = w; n.trigger.delay = windowOf(w).delay })}
                options={WINDOWS.map((w) => [w.size, w.label] as [string, string])} />
            </Field>
          ) : null}
          <Field className="col-span-2" label="PromQL"
            hint={snapshot ? 'Use __USAGE_DATE__ for the day. Each run exports the previous UTC day; Sample and Preview let you pick one.' : `Use [__WINDOW__] as the range; it becomes [${d.trigger.window}].`}>
            <Textarea className="min-h-28" spellCheck={false} value={d.source.query} onChange={(e) => update((n) => { n.source.query = e.target.value })}
              placeholder={snapshot ? 'sum by (customer_id, usage_date) (daily_usage{usage_date="__USAGE_DATE__"})' : 'sum by (customer_id) (increase(api_requests_total[__WINDOW__]))'} />
          </Field>
        </div>
      </Section>
      <Section title="Sample" flush>
        <div className="flex flex-wrap items-center gap-2 border-b px-5 py-3">
          {snapshot ? <DayPicker value={day} max={toInput(newest).slice(0, 10)} onChange={(v) => setRange({ ...range, day: v })} /> : <>
            <DateTime label="From" size={d.trigger.window} value={from} max={to} onChange={(v) => setRange({ ...range, from: v, to })} />
            <DateTime label="To" size={d.trigger.window} value={to} max={toInput(end)} onChange={(v) => setRange({ ...range, from, to: v })} />
          </>}
          <span className="text-muted-foreground text-xs">UTC</span>
          <Button onClick={run} disabled={busy || !d.source.query.trim() || !d.source.connection}>{busy ? <Spinner /> : <PlayIcon aria-hidden />} Run query</Button>
          {sample ? <span className="text-muted-foreground ml-auto text-xs">{sample.rows.length}{sample.truncated ? '+' : ''} series</span> : null}
        </div>
        {d.source.query.trim() ? <p className="text-muted-foreground border-b px-5 py-2 text-xs">
          {snapshot ? 'Runs the query for this day, with __USAGE_DATE__ filled in.' : fixed ? 'The query has no [__WINDOW__], so it only covers the window ending at To.' : 'Lists every label set active in the range. Values are totals over the range; Preview shows per-window values.'}
        </p> : null}
        {error ? <div className="p-5"><Alert level="error">{error}</Alert></div> : null}
        {!sample && !error ? <p className="text-muted-foreground px-5 py-6 text-[13px]">Run the query to see its series. The next step uses their labels.</p> : null}
        {sample && sample.rows.length === 0 ? <p className="text-muted-foreground px-5 py-6 text-[13px]">No series in this window. Try another one.</p> : null}
        {sample && sample.rows.length ? (
          <div className="max-h-80 overflow-auto">
            <Table head={[...labels, 'Value']}>
              {sample.rows.slice(0, 100).map((r, i) => (
                <Tr key={i}>
                  {labels.map((l) => <Td key={l} mono>{r.labels[l] ?? '-'}</Td>)}
                  <Td mono className="tabular-nums">{fmtNum(r.value)}</Td>
                </Tr>
              ))}
            </Table>
          </div>
        ) : null}
      </Section>
    </>
  )
}

function MapStep({ d, update, meters, metersError, meter, distinct, sample, sampling }: StepProps & { meters: Meter[] | null; metersError: string; meter?: Meter; distinct: Record<string, string[]>; sample: SampleResult | null; sampling: boolean }) {
  const { connections } = useApp()
  const fps = connections.filter((c) => c.type === 'flexprice')
  const c = d.mapping.customer
  const pickMeter = (id: string) => {
    const m = meters?.find((x) => x.id === id)
    if (m) update((n) => { n.mapping.meter_id = m.id; n.mapping.event_name = m.event_name; n.mapping.value_property = meterValueProperty(m) || 'value' })
  }
  const labelOptions = Object.entries(distinct).map(([l, vs]) => ({ value: l, hint: vs.slice(0, 3).join(', ') }))
  return (
    <>
      <Section title="Event" description="The event your metric is sent as.">
        <div className="grid grid-cols-2 gap-4">
          {fps.length > 1 ? <Field label="Flexprice"><Select value={d.destination} onChange={(e) => update((n) => { n.destination = e.target.value })} options={fps.map((x) => [x.id, x.id] as [string, string])} /></Field> : null}
          <Field label="Event name" group error={metersError || undefined}
            hint={meter ? `Counted by meter ${meter.name}: ${meter.aggregation.type}${meterValueProperty(meter) ? ` of ${meterValueProperty(meter)}` : ''}` : undefined}>
            {meters === null && d.destination ? <span className="text-muted-foreground flex h-8 items-center gap-2 text-sm"><Spinner /> Loading…</span> : (
              <Combobox label="Event name" value={meter?.id ?? ''} onChange={pickMeter} placeholder="Search events" options={(meters ?? []).map((m) => ({ value: m.id, label: m.event_name, hint: m.name }))} emptyText="No events match" />
            )}
          </Field>
        </div>
      </Section>
      <EventProperties d={d} update={update} meter={meter} distinct={distinct} sampling={sampling} />
      <Section title="Customer" flush>
        <div className="flex flex-col gap-4 p-5">
          <div className="bg-muted flex w-fit rounded-lg p-0.5" role="radiogroup" aria-label="Customer">
            {([['label', 'From a label'], ['static', 'One customer']] as const).map(([k, l]) => (
              <button key={k} type="button" role="radio" aria-checked={c.from === k} onClick={() => update((n) => { n.mapping.customer.from = k })}
                className={cn('h-7 rounded-md px-3 text-[13px]', c.from === k ? 'bg-background font-medium shadow-xs' : 'text-muted-foreground')}>{l}</button>
            ))}
          </div>
          <div className="grid grid-cols-2 gap-4">
            {c.from === 'static'
              ? <Field label="Customer" group><CustomerPicker dest={d.destination} label="Customer" value={c.value ?? ''} onChange={(v) => update((n) => { n.mapping.customer.value = v })} placeholder="Search customers" /></Field>
              : <Field label="Label" group hint="Its value is sent as the customer ID. Filter out customers in the PromQL.">
                  <Combobox label="Customer label" value={c.label ?? ''} onChange={(v) => update((n) => { n.mapping.customer.label = v })} options={labelOptions} allowCustom placeholder="customer_id" emptyText={sampling ? 'Loading labels…' : 'No labels found; type one'} />
                </Field>}
          </div>
        </div>
        {c.from === 'label' && c.label ? <CustomerValues dest={d.destination} values={distinct[c.label] ?? []} /> : null}
      </Section>
      <ExampleEvent d={d} sample={sample} />
      <More title="More options: timestamp">
        <Field label="Timestamp" className="w-72" hint={d.mapping.timestamp === 'window_end' ? 'The last window of a billing period counts toward the next period.' : 'Usage stays in the billing period it belongs to.'}>
          <Select value={d.mapping.timestamp} onChange={(e) => update((n) => { n.mapping.timestamp = e.target.value as Collector['mapping']['timestamp'] })} options={[['window_start', 'Window start'], ['window_end', 'Window end']]} />
        </Field>
      </More>
    </>
  )
}

// ExampleEvent shows the event the first sample row would become with the
// current mapping. Preview runs the real pipeline and shows exact events.
function ExampleEvent({ d, sample }: { d: Collector; sample: SampleResult | null }) {
  const row = sample?.rows[0]
  const m = d.mapping, c = m.customer
  const size = windowOf(d.trigger.window).seconds * 1000
  const end = sample ? new Date(sample.window.end) : null
  const start = end ? new Date(end.getTime() - size) : null
  const iso = (x: Date | null) => (x ? x.toISOString().replace('.000Z', 'Z') : '<window start>')
  const key = row && c.label ? row.labels[c.label] : undefined
  // A range sample's values are totals over the range, not one window.
  const ranged = !!sample && new Date(sample.window.end).getTime() - new Date(sample.window.start).getTime() > size
  const event = {
    event_id: 'col_<hash>',
    event_name: m.event_name || '<pick an event>',
    external_customer_id: c.from === 'static' ? c.value || '<customer>' : key || `<${c.label || 'label'}>`,
    timestamp: m.timestamp === 'window_end' ? iso(end) : iso(start),
    source: `collector:${d.id || '<id>'}`,
    properties: { ...Object.fromEntries(Object.entries(m.property_labels ?? {}).map(([k, l]) => [k, row?.labels[l] ?? `<${l}>`])), ...(m.properties ?? {}), [m.value_property || 'value']: row && !ranged ? row.value : '<value for one window>' },
  }
  return (
    <Section title="Example event" description={row ? 'The first sample row with this mapping. Preview shows the exact events.' : 'Run the query on the Query step to fill this in with a real row.'}>
      <Code className="max-h-80 text-[11.5px]">{JSON.stringify(event, null, 2)}</Code>
    </Section>
  )
}

type PropRow = { key: string; mode: 'label' | 'default'; value: string }

// EventProperties lists the properties sent with each event, added on demand:
// each row names a property and takes its value from a label of the query or a
// default value. Rows live locally; complete ones are saved.
function EventProperties({ d, update, meter, distinct, sampling }: StepProps & { meter?: Meter; distinct: Record<string, string[]>; sampling: boolean }) {
  const m = d.mapping
  const labels = Object.keys(distinct)
  const [rows, setRows] = useState<PropRow[]>(() => [
    ...Object.entries(m.property_labels ?? {}).map(([key, l]) => ({ key, mode: 'label' as const, value: l })),
    ...Object.entries(m.properties ?? {}).map(([key, v]) => ({ key, mode: 'default' as const, value: v })),
  ])
  const write = (next: PropRow[]) => {
    setRows(next)
    const done = next.filter((x) => x.key.trim() && x.value.trim())
    update((n) => {
      n.mapping.property_labels = Object.fromEntries(done.filter((x) => x.mode === 'label').map((x) => [x.key.trim(), x.value.trim()]))
      n.mapping.properties = Object.fromEntries(done.filter((x) => x.mode === 'default').map((x) => [x.key.trim(), x.value.trim()]))
    })
  }
  const set = (i: number, patch: Partial<PropRow>) => write(rows.map((x, j) => (j === i ? { ...x, ...patch } : x)))
  // Label rows added before the labels loaded take the first label once they arrive.
  useEffect(() => {
    if (labels.length && rows.some((x) => x.mode === 'label' && !x.value)) write(rows.map((x) => (x.mode === 'label' && !x.value ? { ...x, value: labels[0] } : x)))
  }, [labels.join('\n')]) // eslint-disable-line react-hooks/exhaustive-deps
  const keys = rows.map((x) => x.key.trim())
  const filterKeys = (meter?.filters ?? []).map((f) => f.key)
  const keyOptions = filterKeys.map((k) => ({ value: k, hint: 'meter filter' }))
  return (
    <Section title="Event properties" flush
      description={`Only the properties listed here are sent with each event, besides the value.${filterKeys.length ? ` The meter filters on ${filterKeys.join(', ')}.` : ''}`}>
      {rows.length ? (
        <div className="flex flex-col">
          <div className="text-muted-foreground grid grid-cols-[minmax(0,2fr)_minmax(0,3fr)_2.25rem] gap-3 border-b px-5 py-2 text-xs font-medium">
            <span>Property</span><span>Value</span><span />
          </div>
          {rows.map((x, i) => {
            const dup = !!x.key.trim() && keys.indexOf(x.key.trim()) !== i
            const note = dup ? 'Duplicate property; only the last row is used.'
              : x.mode === 'label' && x.value && labels.length && !labels.includes(x.value) ? `Label ${x.value} is not in the query; rows without it are rejected.`
              : x.value && !x.key.trim() ? 'Name the property.' : ''
            const options: [string, string][] = [
              ...(x.value && !labels.includes(x.value) ? [[x.value, `${x.value}  (not in the query)`] as [string, string]] : []),
              ...labels.map((l) => [l, l] as [string, string]),
            ]
            const seen = x.mode === 'label' ? distinct[x.value] ?? [] : []
            return (
              <div key={i} className="flex flex-col gap-1 border-b px-5 py-2.5 last:border-b-0">
                <div className="grid grid-cols-[minmax(0,2fr)_minmax(0,3fr)_2.25rem] items-start gap-3">
                  <Combobox label={`Property ${i + 1}`} value={x.key} onChange={(v) => set(i, { key: v })} options={keyOptions} allowCustom customHint="property name" placeholder="region" emptyText="Type a property name" />
                  <div className="flex min-w-0 items-start gap-2">
                    <div className="bg-muted flex shrink-0 rounded-lg p-0.5" role="radiogroup" aria-label={`Value type for ${x.key || `property ${i + 1}`}`}>
                      {(['label', 'default'] as const).map((k) => (
                        <button key={k} type="button" role="radio" aria-checked={x.mode === k} onClick={() => x.mode !== k && set(i, { mode: k, value: k === 'label' ? labels[0] ?? '' : '' })}
                          className={cn('h-7 rounded-md px-2.5 text-xs capitalize', x.mode === k ? 'bg-background font-medium shadow-xs' : 'text-muted-foreground')}>{k}</button>
                      ))}
                    </div>
                    <div className="flex min-w-0 flex-1 flex-col gap-1.5">
                      {x.mode === 'default'
                        ? <Input className="min-w-0" value={x.value} placeholder="Default value, e.g. us" aria-label={`Default value for ${x.key || `property ${i + 1}`}`} onChange={(e) => set(i, { value: e.target.value })} />
                        : !options.length
                          ? <Input className="min-w-0 font-mono" value={x.value} placeholder={sampling ? 'Loading labels…' : 'label name'} aria-label={`Label for ${x.key || `property ${i + 1}`}`} onChange={(e) => set(i, { value: e.target.value })} />
                          : <Select className="min-w-0" value={x.value} aria-label={`Label for ${x.key || `property ${i + 1}`}`} onChange={(e) => set(i, { value: e.target.value })} options={options} />}
                      {seen.length ? (
                        <div className="flex flex-wrap items-center gap-1" aria-label={`Sample values of ${x.value}`}>
                          {seen.slice(0, 20).map((v) => <span key={v} className="bg-muted rounded-md px-1.5 py-0.5 font-mono text-[11px]">{v}</span>)}
                          {seen.length > 20 ? <span className="text-muted-foreground text-[11px]">+{seen.length - 20} more</span> : null}
                        </div>
                      ) : null}
                    </div>
                  </div>
                  <Button variant="ghost" size="icon" title="Delete" aria-label={`Delete ${x.key || `property ${i + 1}`}`} onClick={() => write(rows.filter((_, j) => j !== i))}><XIcon aria-hidden /></Button>
                </div>
                {note ? <p className="text-warning-foreground text-xs">{note}</p> : null}
              </div>
            )
          })}
        </div>
      ) : null}
      <div className={cn('px-5 py-3', rows.length && 'border-t')}>
        <Button variant="outline" size="sm" onClick={() => setRows([...rows, { key: '', mode: 'label', value: labels[0] ?? '' }])}><PlusIcon aria-hidden /> Add property</Button>
      </div>
    </Section>
  )
}

function CustomerPicker({ dest, value, onChange, placeholder, label }: { dest: string; value: string; onChange: (v: string) => void; placeholder?: string; label: string }) {
  const [opts, setOpts] = useState<Customer[]>([])
  const query = (q: string) => { api<Customer[] | null>('GET', `/api/connections/${dest}/customers?q=${encodeURIComponent(q)}`).then((r) => setOpts(r ?? [])).catch(() => setOpts([])) }
  return (
    <Combobox label={label} value={value} onChange={onChange} placeholder={placeholder} allowCustom onQuery={dest ? query : undefined} emptyText="No customers match"
      options={opts.map((x) => ({ value: x.external_id, label: x.name || x.external_id, hint: x.name && x.name !== x.external_id ? x.external_id : undefined }))} />
  )
}

// CustomerValues lists the customer label's values in the sample, each sent as
// is as the customer ID, and whether Flexprice has that customer.
function CustomerValues({ dest, values }: { dest: string; values: string[] }) {
  const [existing, setExisting] = useState<Record<string, boolean>>({})
  const key = values.join('\n')
  useEffect(() => {
    if (!dest || values.length === 0) return
    const h = window.setTimeout(() => {
      api<{ existing: Record<string, boolean> }>('POST', `/api/connections/${dest}/customers/check`, { external_ids: values })
        .then((r) => setExisting(Object.fromEntries(values.map((x) => [x, !!r.existing[x]])))).catch(() => {})
    }, 300)
    return () => window.clearTimeout(h)
  }, [key, dest]) // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <div className="border-t">
      <div className="px-5 pt-4 pb-2">
        <p className="text-[13px] font-medium">Customer IDs in the sample</p>
        <p className="text-muted-foreground mt-0.5 text-[13px]">Values not in Flexprice are still sent, and bill once that customer exists.</p>
      </div>
      {values.length === 0 ? <p className="text-muted-foreground px-5 pb-4 text-[13px]">Run the query on the Query step to list them.</p> : (
        <Table head={['Customer ID', 'Status']}>
          {values.slice(0, 200).map((v) => (
            <Tr key={v}>
              <Td mono>{v}</Td>
              <Td>
                {existing[v] === undefined ? null : existing[v]
                  ? <span className="text-success flex items-center gap-1 text-xs whitespace-nowrap"><CheckCircle2Icon className="size-3.5" aria-hidden /> Customer found</span>
                  : <span className="text-warning-foreground flex items-center gap-1 text-xs whitespace-nowrap" title="Events are still sent and stored, and bill once this customer exists."><TriangleAlertIcon className="size-3.5" aria-hidden /> Not in Flexprice</span>}
              </Td>
            </Tr>
          ))}
        </Table>
      )}
    </div>
  )
}

function ScheduleStep({ d, update }: StepProps) {
  const cp = d.trigger.completeness
  const mins = windowOf(d.trigger.window).seconds / 60
  return (
    <>
      <Section title="Schedule">
        <p className="text-[13px]">Runs {runsAt(d.trigger)}.</p>
      </Section>
      <More title="More options: completeness check">
        <label className="flex items-center gap-2 text-[13px]">
          <Switch label="Completeness check" checked={!!cp} onChange={(on) => update((n) => { n.trigger.completeness = on ? { query: `count_over_time(up{job="my-exporter"}[__WINDOW__]) / ${mins}`, min: 0.999 } : undefined })} />
          Skip a window if data is incomplete
        </label>
        {cp ? (
          <div className="grid grid-cols-[1fr_120px] gap-4">
            <Field label="Query" hint="Every value must reach the minimum, or the window is not sent."><Textarea spellCheck={false} value={cp.query} onChange={(e) => update((n) => { n.trigger.completeness!.query = e.target.value })} /></Field>
            <Field label="Minimum"><Input type="number" step="0.001" value={cp.min} onChange={(e) => update((n) => { n.trigger.completeness!.min = Number(e.target.value) })} /></Field>
          </div>
        ) : null}
      </More>
    </>
  )
}

function PreviewStep({ d, saved, win, setWin, run, preview, fresh, onSave }: { d: Collector; saved: Collector | null; win: string; setWin: (w: string) => void; run: () => Promise<void>; preview: PreviewResult | null; fresh: boolean; onSave: () => Promise<void> }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [force, setForce] = useState(false)
  const go = async () => { setBusy(true); setError(''); try { await run() } catch (e) { setError((e as Error).message) } finally { setBusy(false) } }
  const save = async () => { setSaving(true); setError(''); try { await onSave() } catch (e) { setError((e as Error).message) } finally { setSaving(false) } }
  const p = fresh ? preview : null
  const events = p?.rows.filter((r) => !r.error) ?? []
  const rejects = p?.rows.filter((r) => r.error) ?? []
  const vp = d.mapping.value_property
  const blocking = !!p?.checks.some((c) => c.level === 'error')
  const unchanged = !!saved && canonical(saved) === canonical(d)
  return (
    <>
      <Section title="Preview" flush>
        <div className="flex flex-wrap items-center gap-2 border-b px-5 py-3">
          <WindowPicker size={d.trigger.window} delay={d.trigger.delay} value={win} onChange={setWin} snapshot={d.source.mode === 'usage_date'} />
          <Button onClick={go} disabled={busy}>{busy ? <Spinner /> : <PlayIcon aria-hidden />} Run preview</Button>
          {p ? <span className="text-muted-foreground ml-auto text-xs">{p.events} events · {p.rejected} rejected</span> : null}
        </div>
        <div className="flex flex-col gap-2 p-5">
          {error ? <Alert level="error">{error}</Alert> : null}
          {preview && !fresh ? <Alert level="warning">Changed since the last preview. Run it again.</Alert> : null}
          {!preview && !error ? <p className="text-muted-foreground text-[13px]">Runs the query and mapping for one window and checks Flexprice. Nothing is sent.</p> : null}
          {p?.checks.filter((c) => c.level !== 'ok').map((c, i) => <Alert key={i} level={c.level}>{c.message}</Alert>)}
        </div>
        {events.length ? (
          <Table head={['Customer', vp, 'Properties', 'Timestamp']}>
            {events.slice(0, 200).map((r) => {
              const { [vp]: value, ...rest } = r.event!.properties
              return (
                <Tr key={r.event!.event_id}>
                  <Td className="font-medium">{r.event!.external_customer_id}</Td>
                  <Td className="tabular-nums">{value}</Td>
                  <Td><div className="flex flex-wrap gap-1">{Object.entries(rest).map(([k, v]) => <Badge key={k} className="font-mono">{k}={v}</Badge>)}</div></Td>
                  <Td mono className="whitespace-nowrap">{r.event!.timestamp}</Td>
                </Tr>
              )
            })}
          </Table>
        ) : null}
        {rejects.length ? (
          <Table head={['Rejected', 'Row']}>
            {rejects.slice(0, 200).map((r, i) => (
              <Tr key={i}>
                <Td><Badge tone="error">{r.error}</Badge></Td>
                <Td mono className="break-all">{JSON.stringify(r.row?.labels ?? r.row?.metric ?? r.row)} → {r.row?.value?.toString()}</Td>
              </Tr>
            ))}
          </Table>
        ) : null}
      </Section>
      {events.length ? (
        <Section title="Sample event" description="The first event this window would send.">
          <Code className="max-h-96 text-[11.5px]">{JSON.stringify(events[0].event, null, 2)}</Code>
        </Section>
      ) : null}
      <div className="flex flex-wrap items-start justify-between gap-3 text-[13px]">
        <div className="flex min-w-0 flex-1 flex-col gap-2">
          {!p ? <p className="text-muted-foreground">Run a preview, then save. Saving does not change what runs until you publish.</p> : null}
          {p && blocking ? <label className="flex items-center gap-2"><input type="checkbox" checked={force} onChange={(e) => setForce(e.target.checked)} /> The preview has errors; save anyway</label> : null}
          {saved && !unchanged ? <details><summary className="text-muted-foreground cursor-pointer">Changes from v{saved.version}</summary><div className="mt-2"><DiffView before={canonical(saved)} after={canonical(d)} /></div></details> : null}
        </div>
        <Button onClick={save} disabled={saving || unchanged || !p || (blocking && !force)}>{saving ? <Spinner /> : <SaveIcon aria-hidden />} {saved ? `Save as v${saved.version! + 1}` : 'Save v1'}</Button>
      </div>
    </>
  )
}

function DiffView({ before, after }: { before: string; after: string }) {
  const lines = useMemo(() => lineDiff(before, after), [before, after])
  if (!lines.some((l) => l[0] !== ' ')) return <p className="text-muted-foreground text-[13px]">No changes.</p>
  const keep = lines.map((l, i) => l[0] !== ' ' || lines.slice(Math.max(0, i - 2), i + 3).some((n) => n[0] !== ' '))
  return (
    <Code className="p-0">
      {lines.map(([k, t], i) => keep[i] ? (
        <div key={i} className={cn('px-3', k === '+' && 'bg-success/10 text-success-foreground', k === '-' && 'bg-destructive/10 text-destructive-foreground')}>
          <span className="mr-2 inline-block w-3 select-none opacity-60">{k}</span>{t}
        </div>
      ) : null)}
    </Code>
  )
}

function Versions({ id, published, onPublish, onEdit }: { id: string; published?: number; onPublish: (version: number) => Promise<void>; onEdit: (c: Collector) => void }) {
  const [vs, setVs] = useState<Collector[] | null>(null)
  const [open, setOpen] = useState<number | null>(null)
  useEffect(() => { api<Collector[]>('GET', `/api/collectors/${id}/versions`).then(setVs).catch(() => setVs([])) }, [id])
  if (!vs) return <div className="text-muted-foreground flex items-center gap-2 text-sm"><Spinner /> Loading versions…</div>
  return (
    <Section title="Versions" description="Publish an older version to roll back." flush>
      {vs.length === 0 ? <Empty icon={<HistoryIcon />} title="No versions" /> : (
        <Table head={['Version', 'Saved', 'Event', '', '']}>
          {vs.map((v, i) => (
            <Fragment key={v.version}>
              <Tr>
                <Td><span className="flex items-center gap-2 font-medium">v{v.version} {v.version === published ? <Badge tone="success" dot>Published</Badge> : null}</span></Td>
                <Td className="text-muted-foreground">{new Date(v.saved_at!).toLocaleString()}</Td>
                <Td mono>{v.mapping.event_name}</Td>
                <Td><Button variant="link" size="sm" onClick={() => setOpen(open === v.version ? null : v.version!)}>{open === v.version ? 'Hide' : i === vs.length - 1 ? 'Show' : `Diff vs v${vs[i + 1].version}`}</Button></Td>
                <Td className="text-right">
                  <span className="inline-flex gap-2">
                    {i > 0 ? <Button variant="ghost" size="sm" onClick={() => onEdit(v)}><RotateCcwIcon aria-hidden /> Edit from this</Button> : null}
                    {v.version !== published ? <Button variant="outline" size="sm" onClick={() => onPublish(v.version!)}><RocketIcon aria-hidden /> Publish</Button> : null}
                  </span>
                </Td>
              </Tr>
              {open === v.version ? <tr><td colSpan={5} className="px-4 pb-4"><DiffView before={canonical(vs[i + 1] ?? null)} after={canonical(v)} /></td></tr> : null}
            </Fragment>
          ))}
        </Table>
      )}
    </Section>
  )
}

function RenderedConfig({ draft }: { draft: Collector }) {
  const [y, setY] = useState<string>('')
  useEffect(() => { api<{ yaml: string }>('POST', '/api/render', { collector: spec(draft) }).then((r) => setY(r.yaml)).catch((e) => setY(String(e.message))) }, [draft])
  return (
    <Section title="Bento config" description="What Publish writes. Secrets appear as ${ENV} placeholders.">
      {y ? <Code>{y}</Code> : <Spinner />}
    </Section>
  )
}

function Fragment({ children }: { children: ReactNode }) { return <>{children}</> }
