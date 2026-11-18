import { useState } from 'react'
import { CableIcon, PlugZapIcon } from 'lucide-react'
import { useApp } from '@/App'
import { api, type Connection } from '@/lib'
import { Alert, Badge, Button, Empty, PageHeader, Section, Spinner } from '@/components/ui'

export function ConnectionsPage() {
  const { connections } = useApp()
  return (
    <div>
      <PageHeader title="Connections" meta="Where usage is read from and where events are sent. They come from the deployment's connections file; secrets are environment variables." />
      <div className="px-8 pb-4"><Alert level="info">To add or change a connection, edit the connections file (COLLECTOR_CONNECTIONS_FILE) and redeploy.</Alert></div>
      <div className="grid gap-4 px-8 pb-10 lg:grid-cols-2">
        {connections.length === 0 ? (
          <Section title="No connections" className="lg:col-span-2" flush>
            <Empty icon={<CableIcon />} title="The connections file is empty" description="List your Prometheus (the source) and your Flexprice environment (the destination) in it." />
          </Section>
        ) : connections.map((c) => <ConnectionCard key={c.id} c={c} />)}
      </div>
    </div>
  )
}

function ConnectionCard({ c }: { c: Connection }) {
  const [test, setTest] = useState<{ ok: boolean; message: string } | 'running' | null>(null)
  const run = async () => {
    setTest('running')
    try { setTest(await api('POST', `/api/connections/${c.id}/test`)) } catch (e) { setTest({ ok: false, message: (e as Error).message }) }
  }
  return (
    <Section
      title={c.id}
      description={<span className="font-mono">{c.url}</span>}
      actions={<Button variant="outline" size="sm" onClick={run} disabled={test === 'running'}>{test === 'running' ? <Spinner /> : <PlugZapIcon aria-hidden />} Test</Button>}
    >
      <div className="flex flex-wrap items-center gap-2 text-[13px]">
        <Badge tone="info">{c.type === 'prometheus' ? 'Prometheus · source' : 'Flexprice · destination'}</Badge>
        <Badge>{c.auth_type === 'basic' ? `Basic auth · ${c.username}` : c.auth_type === 'bearer' ? 'Bearer token' : c.auth_type === 'api_key' ? 'API key' : 'No auth'}</Badge>
        {c.secret_env ? <Badge tone="success" dot>Secret from ${c.secret_env}</Badge> : null}
        {Object.keys(c.headers ?? {}).length ? <Badge>{Object.keys(c.headers ?? {}).length} header(s)</Badge> : null}
      </div>
      {test && test !== 'running' ? <Alert className="mt-3" level={test.ok ? 'ok' : 'error'}>{test.message}</Alert> : null}
    </Section>
  )
}
