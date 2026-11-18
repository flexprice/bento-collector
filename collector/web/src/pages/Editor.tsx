import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { CheckIcon, ChevronRightIcon, HistoryIcon, PlayIcon, RotateCcwIcon, SaveIcon, XIcon } from 'lucide-react'
import { useApp } from '@/App'
import { api, canonical, cn, lineDiff, parseList, relTime, yesterday, type Collector, type PreviewResult } from '@/lib'
import { Stepper, StepperDescription, StepperIndicator, StepperItem, StepperNav, StepperSeparator, StepperTitle, StepperTrigger } from '@/components/reui/stepper'
import { Alert, Badge, Button, Code, Empty, Field, Input, KeyValueInput, PageHeader, Section, Select, Spinner, Stat, Switch, Table, Td, Textarea, Tr } from '@/components/ui'

type Step = 'source' | 'mapping' | 'schedule' | 'preview' | 'save'
const STEPS: [Step, string, string][] = [
  ['source', 'Source', 'Connection and PromQL'],
  ['mapping', 'Mapping', 'Rows → Flexprice events'],
  ['schedule', 'Schedule & checks', 'When it runs, what blocks it'],
  ['preview', 'Preview', 'Run against live data'],
  ['save', 'Review & save', 'Diff and new version'],
]

function blank(connections: ReturnType<typeof useApp>['connections']): Collector {
  return {
    id: '', name: '',
    source: { type: 'prometheus', connection: connections.find((c) => c.type === 'prometheus')?.id ?? '', query: '', mode: 'window' },
    trigger: { schedule: '15 0 * * *', window: '1d' },
    mapping: { event_name: '', customer: { from: 'label', label: '', lookup: {} }, unit: '', required_labels: [], drop_labels: [], properties: {}, timestamp: 'window_start' },
    destination: connections.find((c) => c.type === 'flexprice')?.id ?? '',
  }
}

export function CollectorEditor({ id }: { id: string | null }) {
  const { connections, toast, reload, go } = useApp()
  const [saved, setSaved] = useState<Collector | null>(null)
  const [draft, setDraft] = useState<Collector | null>(id ? null : blank(connections))
  const [view, setView] = useState<'configure' | 'versions' | 'config'>('configure')
  const [step, setStep] = useState<Step>('source')
  const [preview, setPreview] = useState<PreviewResult | null>(null)
  const [previewKey, setPreviewKey] = useState('')
  const [day, setDay] = useState(yesterday())

  useEffect(() => {
    if (!id) return
    api<Collector>('GET', `/api/collectors/${id}`).then((c) => { setSaved(c); setDraft(structuredClone(c)) }).catch((e) => toast(e.message, true))
  }, [id, toast])

  if (!draft) return <div className="text-muted-foreground flex items-center gap-2 p-8 text-sm"><Spinner /> Loading…</div>
  const fresh = !!preview && previewKey === canonical(draft)
  const dirty = canonical(saved) !== canonical(draft)
  const update = (fn: (d: Collector) => void) => setDraft((d) => { const n = structuredClone(d!); fn(n); return n })

  const runPreview = async () => {
    const r = await api<PreviewResult>('POST', '/api/preview', { collector: draft, day })
    setPreview(r); setPreviewKey(canonical(draft))
  }
  const save = async () => {
    const { version: _v, saved_at: _s, saved_by: _b, ...body } = draft
    const s = await api<Collector>('PUT', `/api/collectors/${encodeURIComponent(draft.id)}`, body)
    toast(`Saved ${s.id} as v${s.version}`)
    await reload()
    if (!id) { go(`#/collectors/${s.id}`); return }
    setSaved(s); setDraft(structuredClone(s)); setPreviewKey(canonical(s)); setView('versions')
  }

  return (
    <div>
      <PageHeader
        breadcrumb={<a href="#/collectors" className="text-muted-foreground hover:text-foreground flex items-center gap-1 text-xs">Collectors <ChevronRightIcon className="size-3" /> {saved ? saved.id : 'New'}</a>}
        title={<span className="flex items-center gap-2">{draft.name || draft.id || 'New collector'} {saved ? <Badge tone="info">v{saved.version}</Badge> : <Badge>Draft</Badge>}{dirty && saved ? <Badge tone="warning" dot>Unsaved changes</Badge> : null}</span>}
        meta={saved ? `Live version saved ${relTime(saved.saved_at)} · runs ${saved.trigger.schedule} UTC` : 'Query Prometheus once per UTC day and turn each row into a Flexprice event.'}
        actions={saved ? (
          <div className="bg-muted flex rounded-lg p-0.5">
            {(['configure', 'versions', 'config'] as const).map((v) => (
              <button key={v} onClick={() => setView(v)} className={cn('h-7 rounded-md px-3 text-[13px] capitalize', view === v ? 'bg-background font-medium shadow-xs' : 'text-muted-foreground')}>
                {v === 'config' ? 'Rendered config' : v}
              </button>
            ))}
          </div>
        ) : null}
      />
      <div className="px-8 pb-10">
        {view === 'versions' && saved ? <Versions id={saved.id} live={saved.version!} onRollback={(c) => { setDraft({ ...structuredClone(c), version: saved.version }); setView('configure'); setStep('preview'); toast(`Loaded v${c.version}. Preview it, then save to roll back.`) }} /> : null}
        {view === 'config' ? <RenderedConfig draft={draft} day={day} /> : null}
        {view === 'configure' ? (
          <div className="grid gap-6 lg:grid-cols-[220px_minmax(0,1fr)_340px]">
            <Stepper value={STEPS.findIndex((s) => s[0] === step) + 1} onValueChange={(v) => setStep(STEPS[v - 1][0])} orientation="vertical" indicators={{ completed: <CheckIcon className="size-3.5" /> }}>
              <StepperNav>
                {STEPS.map(([k, label, sub], i) => (
                  <StepperItem key={k} step={i + 1} className="relative items-start not-last:flex-1">
                    <StepperTrigger className="items-start gap-2.5 rounded-lg pb-6 text-left">
                      <StepperIndicator>{i + 1}</StepperIndicator>
                      <div className="mt-0.5 flex flex-col gap-1">
                        <StepperTitle>{label}</StepperTitle>
                        <StepperDescription className="text-xs">{sub}</StepperDescription>
                      </div>
                    </StepperTrigger>
                    {i < STEPS.length - 1 ? <StepperSeparator className="group-data-[state=completed]/step:bg-primary absolute inset-y-0 top-7 left-3 -order-1 m-0 -translate-x-1/2 group-data-[orientation=vertical]/stepper-nav:h-[calc(100%-2rem)]" /> : null}
                  </StepperItem>
                ))}
              </StepperNav>
            </Stepper>
            <div className="flex min-w-0 flex-col gap-4">
              {step === 'source' ? <SourceStep d={draft} update={update} isNew={!saved} /> : null}
              {step === 'mapping' ? <MappingStep d={draft} update={update} /> : null}
              {step === 'schedule' ? <ScheduleStep d={draft} update={update} /> : null}
              {step === 'preview' ? <PreviewStep day={day} setDay={setDay} run={runPreview} preview={preview} stale={!!preview && !fresh} /> : null}
              {step === 'save' ? <SaveStep saved={saved} draft={draft} preview={fresh ? preview : null} onSave={save} goPreview={() => setStep('preview')} /> : null}
              <div className="flex justify-between">
                <Button variant="outline" disabled={step === 'source'} onClick={() => setStep(STEPS[STEPS.findIndex((s) => s[0] === step) - 1][0])}>Back</Button>
                {step !== 'save' ? <Button variant="outline" onClick={() => setStep(STEPS[STEPS.findIndex((s) => s[0] === step) + 1][0])}>Continue <ChevronRightIcon aria-hidden /></Button> : null}
              </div>
            </div>
            <EventPreviewPanel d={draft} preview={fresh ? preview : null} />
          </div>
        ) : null}
      </div>
    </div>
  )
}

type StepProps = { d: Collector; update: (fn: (d: Collector) => void) => void }

function SourceStep({ d, update, isNew }: StepProps & { isNew: boolean }) {
  const { connections } = useApp()
  const proms = connections.filter((c) => c.type === 'prometheus')
  return (
    <Section title="Source" description="Which Prometheus to query and what to ask it.">
      <div className="grid grid-cols-2 gap-4">
        <Field label="Collector ID" hint="Lowercase; part of every event_id, so it cannot change later"><Input value={d.id} disabled={!isNew} placeholder="syncron-cpu-hours" onChange={(e) => update((n) => { n.id = e.target.value })} /></Field>
        <Field label="Display name"><Input value={d.name ?? ''} placeholder="Syncron reserved CPU core-hours" onChange={(e) => update((n) => { n.name = e.target.value })} /></Field>
        <Field label="Prometheus connection"><Select value={d.source.connection} onChange={(e) => update((n) => { n.source.connection = e.target.value })} options={[['', 'Select…'], ...proms.map((c) => [c.id, `${c.id} · ${c.url}`] as [string, string])]} /></Field>
        <Field label="Query mode" hint={d.source.mode === 'usage_date' ? 'Use __USAGE_DATE__ in the query; rows must carry a matching usage_date label.' : 'Use [1d] ranges; evaluated at 00:00 UTC right after the day.'}>
          <Select value={d.source.mode} onChange={(e) => update((n) => { n.source.mode = e.target.value as Collector['source']['mode'] })} options={[['window', 'Daily window'], ['usage_date', 'Dated snapshot (usage_date)']]} />
        </Field>
        <Field className="col-span-2" label="PromQL" hint="Keep the labels you need in by (…). Everything else is summed away.">
          <Textarea className="min-h-32" spellCheck={false} value={d.source.query} onChange={(e) => update((n) => { n.source.query = e.target.value })} />
        </Field>
      </div>
    </Section>
  )
}

function MappingStep({ d, update }: StepProps) {
  const { connections } = useApp()
  const m = d.mapping
  return (
    <>
      <Section title="Event" description="What each row becomes in Flexprice.">
        <div className="grid grid-cols-2 gap-4">
          <Field label="Event name" hint="Must match a Flexprice meter"><Input value={m.event_name} placeholder="syncron_cpu_hours" onChange={(e) => update((n) => { n.mapping.event_name = e.target.value })} /></Field>
          <Field label="Unit" hint="Stored as properties.unit"><Input value={m.unit ?? ''} placeholder="core_hours" onChange={(e) => update((n) => { n.mapping.unit = e.target.value })} /></Field>
          <Field label="Event timestamp" hint="Start of day keeps usage in the billing period it belongs to">
            <Select value={m.timestamp} onChange={(e) => update((n) => { n.mapping.timestamp = e.target.value as Collector['mapping']['timestamp'] })} options={[['window_start', 'Start of the day (recommended)'], ['window_end', 'End of the day']]} />
          </Field>
          <Field label="Send to"><Select value={d.destination} onChange={(e) => update((n) => { n.destination = e.target.value })} options={[['', 'Select…'], ...connections.filter((c) => c.type === 'flexprice').map((c) => [c.id, `${c.id} · ${c.url}`] as [string, string])]} /></Field>
        </div>
      </Section>
      <Section title="Customer" description="Which Flexprice customer each row is billed to.">
        <div className="grid grid-cols-2 gap-4">
          <Field label="Customer comes from"><Select value={m.customer.from} onChange={(e) => update((n) => { n.mapping.customer.from = e.target.value as 'label' | 'static' })} options={[['label', 'A Prometheus label'], ['static', 'One fixed customer']]} /></Field>
          {m.customer.from === 'static'
            ? <Field label="external_customer_id"><Input value={m.customer.value ?? ''} placeholder="cust_acme" onChange={(e) => update((n) => { n.mapping.customer.value = e.target.value })} /></Field>
            : <Field label="Label" hint="Must survive the query's by (…)"><Input value={m.customer.label ?? ''} placeholder="label_tags_syncron_com_sy_tenant" onChange={(e) => update((n) => { n.mapping.customer.label = e.target.value })} /></Field>}
          {m.customer.from === 'label' ? <>
            <Field className="col-span-2" label="Label value → external_customer_id" group hint="No rows: the label value is the customer ID. Values not listed are rejected.">
              <KeyValueInput value={m.customer.lookup} keyLabel="Label value" valueLabel="external_customer_id" keyPlaceholder="trn_tQAAE7UDDQTNFS" valuePlaceholder="cust_acme" addLabel="Add mapping" onChange={(v) => update((n) => { n.mapping.customer.lookup = v })} />
            </Field>
            <Field className="col-span-2" label="Label values not billed" group hint="Internal or trial tenants: counted as skipped, so they do not fail the daily run.">
              <ListInput value={m.customer.skip} placeholder="Add a tenant, press Enter" onChange={(v) => update((n) => { n.mapping.customer.skip = v })} />
            </Field>
          </> : null}
        </div>
      </Section>
      <Section title="Labels and properties" description="Every label left on a row becomes an event property.">
        <div className="grid grid-cols-2 gap-4">
          <Field label="Required labels" group hint="Rows missing one are rejected"><ListInput value={m.required_labels} placeholder="Add a label, press Enter" onChange={(v) => update((n) => { n.mapping.required_labels = v })} /></Field>
          <Field label="Labels to drop" group><ListInput value={m.drop_labels} placeholder="Add a label, press Enter" onChange={(v) => update((n) => { n.mapping.drop_labels = v })} /></Field>
          <Field className="col-span-2" label="Extra properties" group hint="Fixed values added to every event"><KeyValueInput value={m.properties} keyLabel="Property" valueLabel="Value" keyPlaceholder="product" valuePlaceholder="dataops" addLabel="Add property" onChange={(v) => update((n) => { n.mapping.properties = v })} /></Field>
        </div>
      </Section>
    </>
  )
}

function ScheduleStep({ d, update }: StepProps) {
  const cp = d.trigger.completeness
  return (
    <>
      <Section title="Schedule" description="Runs one closed UTC day at a time.">
        <div className="grid grid-cols-2 gap-4">
          <Field label="Expected schedule (cron, UTC)" hint="Your CronJob / ECS schedule starts runs; keep it in line. 15 0 * * * = 00:15 daily."><Input value={d.trigger.schedule} onChange={(e) => update((n) => { n.trigger.schedule = e.target.value })} /></Field>
          <Field label="Window"><Input value="1 day (UTC midnight to midnight)" disabled /></Field>
        </div>
      </Section>
      <Section title="Completeness gate" description="Do not bill a day that was only partly measured." actions={<Switch label="Completeness gate" checked={!!cp} onChange={(on) => update((n) => { n.trigger.completeness = on ? { query: 'count_over_time(dp_pod_reservation_rule_evaluated[1d]) / 1440', min: 0.999 } : undefined })} />}>
        {cp ? (
          <div className="grid grid-cols-[1fr_140px] gap-4">
            <Field label="Completeness query" hint="Evaluated at the end of the day. Below the minimum, the run sends nothing and fails so the scheduler retries."><Textarea spellCheck={false} value={cp.query} onChange={(e) => update((n) => { n.trigger.completeness!.query = e.target.value })} /></Field>
            <Field label="Minimum"><Input type="number" step="0.001" value={cp.min} onChange={(e) => update((n) => { n.trigger.completeness!.min = Number(e.target.value) })} /></Field>
          </div>
        ) : <p className="text-muted-foreground text-[13px]">Off. Partial days will be sent as they are.</p>}
      </Section>
    </>
  )
}

function PreviewStep({ day, setDay, run, preview, stale }: { day: string; setDay: (d: string) => void; run: () => Promise<void>; preview: PreviewResult | null; stale: boolean }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const go = async () => { setBusy(true); setError(''); try { await run() } catch (e) { setError((e as Error).message) } finally { setBusy(false) } }
  const events = preview?.rows.filter((r) => !r.error) ?? []
  const rejects = preview?.rows.filter((r) => r.error) ?? []
  return (
    <>
      <Section title="Preview" description="Runs the real query and mapping against live Prometheus and checks Flexprice. Nothing is sent."
        actions={<>
          <Input type="date" className="w-40" value={day} max={yesterday()} onChange={(e) => setDay(e.target.value)} aria-label="Day" />
          <Button onClick={go} disabled={busy}>{busy ? <Spinner /> : <PlayIcon aria-hidden />} Run preview</Button>
        </>}>
        {error ? <Alert level="error">{error}</Alert> : null}
        {stale ? <Alert level="warning" className="mb-3">The collector changed since this preview. Run it again before saving.</Alert> : null}
        {preview ? (
          <div className="flex flex-col gap-4">
            <div className="grid grid-cols-4 gap-3">
              <Stat label="Events ready" value={preview.events} tone={preview.events ? 'success' : undefined} />
              <Stat label="Rejected" value={preview.rejected} tone={preview.rejected ? 'error' : undefined} />
              <Stat label="Skipped" value={preview.skipped} />
              <Stat label="Day completeness" value={preview.completeness ? (preview.completeness.values.length ? `${(Math.min(...preview.completeness.values) * 100).toFixed(1)}%` : 'No data') : '—'} tone={preview.completeness ? (preview.completeness.ok ? 'success' : 'error') : undefined}
                hint={preview.completeness ? (preview.completeness.ok ? 'Day fully measured' : `Below the ${preview.completeness.min * 100}% minimum`) : 'No gate configured'} />
            </div>
            <div className="flex flex-col gap-2">{preview.checks.map((c, i) => <Alert key={i} level={c.level}>{c.message}</Alert>)}</div>
          </div>
        ) : !error ? <Empty icon={<PlayIcon />} title="No preview yet" description={`Pick a finished UTC day and run the collector against it. Default: ${yesterday()}.`} /> : null}
      </Section>
      {events.length ? (
        <Section title={`Events that would be sent · ${preview!.window.start.slice(0, 10)}`} flush>
          <Table head={['Customer', 'Value', 'Labels', 'Timestamp', 'event_id']}>
            {events.slice(0, 200).map((r) => {
              const { value, unit, window_start: _a, window_end: _b, window_seconds: _c, ...rest } = r.event!.properties
              return (
                <Tr key={r.event!.event_id}>
                  <Td className="font-medium">{r.event!.external_customer_id}</Td>
                  <Td className="tabular-nums whitespace-nowrap">{value} <span className="text-muted-foreground">{unit}</span></Td>
                  <Td><div className="flex flex-wrap gap-1">{Object.entries(rest).map(([k, v]) => <Badge key={k} className="font-mono">{k}={v}</Badge>)}</div></Td>
                  <Td mono className="whitespace-nowrap">{r.event!.timestamp}</Td>
                  <Td mono className="text-muted-foreground">{r.event!.event_id.slice(0, 14)}…</Td>
                </Tr>
              )
            })}
          </Table>
        </Section>
      ) : null}
      {rejects.length ? (
        <Section title="Rejected rows · not sent" flush>
          <Table head={['Reason', 'Prometheus row']}>
            {rejects.slice(0, 200).map((r, i) => (
              <Tr key={i}>
                <Td><Badge tone="error">{r.error}</Badge></Td>
                <Td mono className="break-all">{JSON.stringify(r.series?.metric)} → {r.series?.value?.[1]}</Td>
              </Tr>
            ))}
          </Table>
        </Section>
      ) : null}
    </>
  )
}

// Chips: Enter, comma or blur adds; Backspace on an empty box removes the last; pasted lists are split.
function ListInput({ value, onChange, placeholder }: { value?: string[]; onChange: (v: string[]) => void; placeholder?: string }) {
  const items = value ?? []
  const [text, setText] = useState('')
  const add = (raw: string) => {
    const next = [...items]
    for (const x of parseList(raw)) if (!next.includes(x)) next.push(x)
    if (next.length !== items.length) onChange(next)
    setText('')
  }
  return (
    <div className="border-input focus-within:border-ring focus-within:ring-ring/50 flex min-h-8 w-full flex-wrap items-center gap-1 rounded-lg border px-1.5 py-1 focus-within:ring-3">
      {items.map((x) => (
        <Badge key={x} className="gap-0.5 pr-0.5 font-mono">
          {x}
          <button type="button" aria-label={`Remove ${x}`} className="hover:bg-foreground/10 rounded-sm p-0.5" onClick={() => onChange(items.filter((y) => y !== x))}><XIcon className="size-3" aria-hidden /></button>
        </Badge>
      ))}
      <input className="placeholder:text-muted-foreground min-w-24 flex-1 bg-transparent px-1 text-sm outline-none" value={text} spellCheck={false}
        placeholder={items.length ? '' : placeholder} aria-label={placeholder}
        onChange={(e) => { if (e.target.value.includes(',')) add(e.target.value); else setText(e.target.value) }}
        onKeyDown={(e) => {
          if (e.key === 'Enter') { e.preventDefault(); add(text) }
          else if (e.key === 'Backspace' && !text && items.length) onChange(items.slice(0, -1))
        }}
        onBlur={() => text.trim() && add(text)} />
    </div>
  )
}

function DiffView({ before, after }: { before: string; after: string }) {
  const lines = useMemo(() => lineDiff(before, after), [before, after])
  const changed = lines.some((l) => l[0] !== ' ')
  if (!changed) return <p className="text-muted-foreground text-[13px]">No changes from the live version.</p>
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

function SaveStep({ saved, draft, preview, onSave, goPreview }: { saved: Collector | null; draft: Collector; preview: PreviewResult | null; onSave: () => Promise<void>; goPreview: () => void }) {
  const [force, setForce] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const blocking = !!preview?.checks.some((c) => c.level === 'error')
  const unchanged = !!saved && canonical(saved) === canonical(draft)
  const save = async () => { setBusy(true); setError(''); try { await onSave() } catch (e) { setError((e as Error).message) } finally { setBusy(false) } }
  return (
    <>
      <Section title="Changes" description={saved ? `Compared with live v${saved.version}` : 'First version'}><DiffView before={canonical(saved)} after={canonical(draft)} /></Section>
      <Section title="Save" actions={<Button onClick={save} disabled={busy || unchanged || !preview || (blocking && !force)}>{busy ? <Spinner /> : <SaveIcon aria-hidden />} {saved ? `Save as v${saved.version! + 1}` : 'Save v1'}</Button>}>
        <div className="flex flex-col gap-3">
          {!preview ? <Alert level="warning" title="Preview required">Run a preview of this exact version before saving. <Button variant="link" size="sm" onClick={goPreview}>Go to preview</Button></Alert> : null}
          {preview && !blocking ? <Alert level="ok" title={`Preview passed for ${preview.window.start.slice(0, 10)}`}>{preview.events} events, {preview.rejected} rejected, {preview.skipped} skipped.</Alert> : null}
          {preview && blocking ? <>
            <Alert level="error" title="The last preview has errors">Saving now means the next run fails on the same day. Fix the mapping or data first.</Alert>
            <label className="flex items-center gap-2 text-[13px]"><input type="checkbox" checked={force} onChange={(e) => setForce(e.target.checked)} /> I understand, save anyway</label>
          </> : null}
          {error ? <Alert level="error">{error}</Alert> : null}
          <p className="text-muted-foreground text-xs">Every save is a new immutable version (committed to git when enabled). The next scheduled run uses the latest version.</p>
        </div>
      </Section>
    </>
  )
}

function EventPreviewPanel({ d, preview }: { d: Collector; preview: PreviewResult | null }) {
  const sample = preview?.rows.find((r) => r.event)?.event
  const shape = sample ?? {
    event_id: 'col_<hash of collector, event, customer, window, labels>',
    event_name: d.mapping.event_name || '<event name>',
    external_customer_id: d.mapping.customer.from === 'static' ? d.mapping.customer.value || '<customer>' : `<${d.mapping.customer.label || 'label'}${Object.keys(d.mapping.customer.lookup ?? {}).length ? ' → lookup' : ''}>`,
    timestamp: d.mapping.timestamp === 'window_start' ? '<day>T00:00:00Z' : '<next day>T00:00:00Z',
    source: `collector:${d.id || '<id>'}`,
    properties: { '<labels>': '…', ...(d.mapping.properties ?? {}), ...(d.mapping.unit ? { unit: d.mapping.unit } : {}), value: '<row value>' },
  }
  return (
    <aside className="flex flex-col gap-4 lg:sticky lg:top-6 lg:self-start">
      <Section title={sample ? 'Sample event' : 'Event shape'} description={sample ? 'First event from the last preview' : 'Run a preview to see a real event'}>
        <Code className="max-h-[420px] text-[11.5px]">{JSON.stringify(shape, null, 2)}</Code>
      </Section>
      <Section title="How a run behaves">
        <ul className="text-muted-foreground flex list-disc flex-col gap-1.5 pl-4 text-xs leading-4">
          <li>Incomplete day, query error or missing meter: nothing is sent, run fails.</li>
          <li>Bad rows (negative, NaN, unknown customer): rejected; valid rows still sent; run fails so you get alerted.</li>
          <li>Re-running a day sends identical event IDs; Flexprice does not double-bill.</li>
        </ul>
      </Section>
    </aside>
  )
}

function Versions({ id, live, onRollback }: { id: string; live: number; onRollback: (c: Collector) => void }) {
  const [vs, setVs] = useState<Collector[] | null>(null)
  const [open, setOpen] = useState<number | null>(null)
  useEffect(() => { api<Collector[]>('GET', `/api/collectors/${id}/versions`).then(setVs).catch(() => setVs([])) }, [id])
  if (!vs) return <div className="text-muted-foreground flex items-center gap-2 text-sm"><Spinner /> Loading versions…</div>
  return (
    <Section title="Version history" description="Each save is immutable. Roll back by loading an older version, previewing it, and saving it as the newest." flush>
      {vs.length === 0 ? <Empty icon={<HistoryIcon />} title="No versions" /> : (
        <Table head={['Version', 'Saved', 'Event', 'Changes', '']}>
          {vs.map((v, i) => (
            <Fragment key={v.version}>
              <Tr>
                <Td><span className="flex items-center gap-2 font-medium">v{v.version} {v.version === live ? <Badge tone="success" dot>Live</Badge> : null}</span></Td>
                <Td className="text-muted-foreground">{new Date(v.saved_at!).toLocaleString()}</Td>
                <Td mono>{v.mapping.event_name}</Td>
                <Td><Button variant="link" size="sm" onClick={() => setOpen(open === v.version ? null : v.version!)}>{open === v.version ? 'Hide diff' : i === vs.length - 1 ? 'Show initial spec' : `Diff vs v${vs[i + 1].version}`}</Button></Td>
                <Td className="text-right">{v.version !== live ? <Button variant="outline" size="sm" onClick={() => onRollback(v)}><RotateCcwIcon aria-hidden /> Roll back to this</Button> : null}</Td>
              </Tr>
              {open === v.version ? <tr><td colSpan={5} className="px-4 pb-4"><DiffView before={canonical(vs[i + 1] ?? null)} after={canonical(v)} /></td></tr> : null}
            </Fragment>
          ))}
        </Table>
      )}
    </Section>
  )
}

function RenderedConfig({ draft, day }: { draft: Collector; day: string }) {
  const [y, setY] = useState<string>('')
  useEffect(() => { api<{ yaml: string }>('POST', '/api/render', { collector: draft, day }).then((r) => setY(r.yaml)).catch((e) => setY(String(e.message))) }, [draft, day])
  return (
    <Section title="Rendered Bento config" description={`Generated from the spec for ${day}. Read-only; runs always render it fresh. Secrets appear as \${ENV} placeholders.`}>
      {y ? <Code>{y}</Code> : <Spinner />}
    </Section>
  )
}

function Fragment({ children }: { children: ReactNode }) { return <>{children}</> }
