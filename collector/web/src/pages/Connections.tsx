import { useState } from 'react'
import { CableIcon, DatabaseIcon, PencilIcon, PlugZapIcon, ReceiptIcon, XIcon } from 'lucide-react'
import { useApp } from '@/App'
import { api, formatKV, parseKV, relTime, type Connection } from '@/lib'
import { Alert, Badge, Button, Empty, Field, Input, PageHeader, Section, Select, Spinner, Textarea } from '@/components/ui'

const blank = (type: Connection['type']): Connection =>
  type === 'flexprice'
    ? { id: 'flexprice', type, url: 'https://api.cloud.flexprice.io', auth_type: 'api_key', headers: {} }
    : { id: '', type, url: '', auth_type: 'none', headers: {} }

export function ConnectionsPage() {
  const { connections } = useApp()
  const [editing, setEditing] = useState<Connection | null>(null)
  return (
    <div>
      <PageHeader
        title="Connections"
        meta="Where usage is read from and where events are sent. Secrets go to the secret store, never into specs or git."
        actions={<>
          <Button variant="outline" onClick={() => setEditing(blank('flexprice'))}><ReceiptIcon aria-hidden /> Add Flexprice</Button>
          <Button onClick={() => setEditing(blank('prometheus'))}><DatabaseIcon aria-hidden /> Add Prometheus</Button>
        </>}
      />
      <div className="grid gap-4 px-8 pb-10 lg:grid-cols-2">
        {connections.length === 0 ? (
          <Section title="No connections" className="lg:col-span-2" flush>
            <Empty icon={<CableIcon />} title="Nothing connected yet" description="Add your Prometheus (the source) and your Flexprice environment (the destination)." />
          </Section>
        ) : connections.map((c) => <ConnectionCard key={c.id} c={c} onEdit={() => setEditing({ ...c })} />)}
      </div>
      {editing ? <ConnectionDialog initial={editing} onClose={() => setEditing(null)} /> : null}
    </div>
  )
}

function ConnectionCard({ c, onEdit }: { c: Connection; onEdit: () => void }) {
  const [test, setTest] = useState<{ ok: boolean; message: string } | 'running' | null>(null)
  const run = async () => {
    setTest('running')
    try { setTest(await api('POST', `/api/connections/${c.id}/test`)) } catch (e) { setTest({ ok: false, message: (e as Error).message }) }
  }
  return (
    <Section
      title={c.id}
      description={<span className="font-mono">{c.url}</span>}
      actions={<>
        <Button variant="outline" size="sm" onClick={run} disabled={test === 'running'}>{test === 'running' ? <Spinner /> : <PlugZapIcon aria-hidden />} Test</Button>
        <Button variant="ghost" size="icon" aria-label={`Edit ${c.id}`} onClick={onEdit}><PencilIcon aria-hidden /></Button>
      </>}
    >
      <div className="flex flex-wrap items-center gap-2 text-[13px]">
        <Badge tone="info">{c.type === 'prometheus' ? 'Prometheus · source' : 'Flexprice · destination'}</Badge>
        <Badge>{c.auth_type === 'basic' ? `Basic auth · ${c.username}` : c.auth_type === 'bearer' ? 'Bearer token' : c.auth_type === 'api_key' ? 'API key' : 'No auth'}</Badge>
        {c.secret_ref ? <Badge tone={c.has_secret ? 'success' : 'error'} dot>{c.has_secret ? 'Secret stored' : 'Secret missing'}</Badge> : null}
        {Object.keys(c.headers ?? {}).length ? <Badge>{Object.keys(c.headers ?? {}).length} header(s)</Badge> : null}
        <span className="text-muted-foreground ml-auto text-xs">Updated {relTime(c.updated_at)}</span>
      </div>
      {test && test !== 'running' ? <Alert className="mt-3" level={test.ok ? 'ok' : 'error'}>{test.message}</Alert> : null}
    </Section>
  )
}

function ConnectionDialog({ initial, onClose }: { initial: Connection; onClose: () => void }) {
  const { connections, reload, toast } = useApp()
  const [c, setC] = useState<Connection>(initial)
  const [secret, setSecret] = useState('')
  const [headers, setHeaders] = useState(formatKV(initial.headers))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const isNew = !connections.some((x) => x.id === initial.id)
  const isProm = c.type === 'prometheus'
  const set = (p: Partial<Connection>) => setC({ ...c, ...p })
  const save = async () => {
    setBusy(true); setError('')
    try {
      const { has_secret: _h, updated_at: _u, ...payload } = c
      await api('PUT', `/api/connections/${encodeURIComponent(c.id)}`, { ...payload, headers: parseKV(headers), ...(secret ? { secret } : {}) })
      await reload(); toast(`Connection ${c.id} saved`); onClose()
    } catch (e) { setError((e as Error).message) } finally { setBusy(false) }
  }
  return (
    <div className="fixed inset-0 z-40 flex items-start justify-center bg-black/30 p-6 pt-[10vh]" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div role="dialog" aria-modal="true" aria-label="Connection" className="bg-background w-full max-w-lg rounded-xl border shadow-lg">
        <div className="flex items-center justify-between border-b px-5 py-3.5">
          <h2 className="text-sm font-medium">{isNew ? `New ${isProm ? 'Prometheus' : 'Flexprice'} connection` : `Edit ${c.id}`}</h2>
          <Button variant="ghost" size="icon" aria-label="Close" onClick={onClose}><XIcon aria-hidden /></Button>
        </div>
        <div className="flex flex-col gap-4 p-5">
          <div className="grid grid-cols-2 gap-4">
            <Field label="ID" hint="Lowercase; collectors refer to it"><Input value={c.id} disabled={!isNew} placeholder={isProm ? 'syncron-prom' : 'flexprice'} onChange={(e) => set({ id: e.target.value })} /></Field>
            <Field label="Authentication">
              <Select value={c.auth_type || 'none'} onChange={(e) => set({ auth_type: e.target.value })}
                options={isProm ? [['none', 'None'], ['basic', 'Basic auth'], ['bearer', 'Bearer token']] : [['api_key', 'API key']]} />
            </Field>
          </div>
          <Field label="URL"><Input value={c.url} placeholder={isProm ? 'https://prometheus.internal:9090' : 'https://api.cloud.flexprice.io'} onChange={(e) => set({ url: e.target.value })} /></Field>
          {c.auth_type === 'basic' ? <Field label="Username"><Input value={c.username ?? ''} onChange={(e) => set({ username: e.target.value })} /></Field> : null}
          {c.auth_type && c.auth_type !== 'none' ? (
            <Field label={isProm ? 'Password / token' : 'API key'} hint={initial.has_secret ? 'Stored. Leave empty to keep the current value.' : 'Written to the secret store; never shown again.'}>
              <Input type="password" autoComplete="new-password" value={secret} placeholder={initial.has_secret ? '••••••••' : ''} onChange={(e) => setSecret(e.target.value)} />
            </Field>
          ) : null}
          <Field label="Extra headers" hint={isProm ? 'One per line, e.g. X-Scope-OrgID = tenant-a. No credentials.' : 'One per line, e.g. x-environment-id = <id> for self-hosted Flexprice.'}>
            <Textarea className="min-h-16" value={headers} onChange={(e) => setHeaders(e.target.value)} />
          </Field>
          {error ? <Alert level="error">{error}</Alert> : null}
        </div>
        <div className="flex justify-end gap-2 border-t px-5 py-3">
          <Button variant="outline" onClick={onClose}>Cancel</Button>
          <Button onClick={save} disabled={busy || !c.id || !c.url}>{busy ? <Spinner /> : null} Save connection</Button>
        </div>
      </div>
    </div>
  )
}
