import { ActivityIcon, PlusIcon } from 'lucide-react'
import { useApp } from '@/App'
import { relTime, windowOf } from '@/lib'
import { Badge, Button, Empty, PageHeader, Section, Table, Td, Tr } from '@/components/ui'

export function CollectorsPage() {
  const { collectors, connections, go } = useApp()
  const hasConnections = connections.some((c) => c.type === 'prometheus') && connections.some((c) => c.type === 'flexprice')
  return (
    <div>
      <PageHeader
        title="Collectors"
        meta="Each published collector runs in Bento once per window and sends one Flexprice event per result row."
        actions={<Button onClick={() => go('#/collectors/new')} disabled={!hasConnections}><PlusIcon aria-hidden /> New collector</Button>}
      />
      <div className="px-8 pb-10">
        <Section title="All collectors" flush>
          {collectors.length === 0 ? (
            <Empty
              icon={<ActivityIcon />}
              title={hasConnections ? 'No collectors yet' : 'Connect Prometheus and Flexprice first'}
              description={hasConnections ? 'Create one to turn a PromQL query into usage events for a Flexprice meter.' : 'A collector reads from a Prometheus connection and sends to a Flexprice connection.'}
              action={hasConnections ? null : <Button variant="outline" onClick={() => go('#/connections')}>Go to connections</Button>}
            />
          ) : (
            <Table head={['Collector', 'Event', 'Window', 'Version', 'Running', 'Updated']}>
              {collectors.map((c) => (
                <Tr key={c.id} onClick={() => go(`#/collectors/${c.id}`)}>
                  <Td>
                    <p className="font-medium">{c.name || c.id}</p>
                    <p className="text-muted-foreground font-mono text-xs">{c.id}</p>
                  </Td>
                  <Td mono>{c.mapping.event_name}</Td>
                  <Td className="text-xs whitespace-nowrap">{c.source.mode === 'usage_date' ? '1 day (snapshot)' : windowOf(c.trigger.window || '1d').short}</Td>
                  <Td><Badge tone="info">v{c.version}</Badge></Td>
                  <Td>{c.published ? <Badge tone="success" dot>v{c.published.version}</Badge> : <Badge>Not published</Badge>}</Td>
                  <Td className="text-muted-foreground whitespace-nowrap">{relTime(c.saved_at)}</Td>
                </Tr>
              ))}
            </Table>
          )}
        </Section>
      </div>
    </div>
  )
}
