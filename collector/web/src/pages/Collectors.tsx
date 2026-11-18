import { ActivityIcon, PlusIcon } from 'lucide-react'
import { useApp } from '@/App'
import { relTime } from '@/lib'
import { Badge, Button, Empty, PageHeader, Section, Table, Td, Tr } from '@/components/ui'

export function CollectorsPage() {
  const { collectors, connections, go } = useApp()
  const hasConnections = connections.some((c) => c.type === 'prometheus') && connections.some((c) => c.type === 'flexprice')
  return (
    <div>
      <PageHeader
        title="Collectors"
        meta="Each collector queries Prometheus once per UTC day and sends one Flexprice event per result row."
        actions={<Button onClick={() => go('#/collectors/new')} disabled={!hasConnections}><PlusIcon aria-hidden /> New collector</Button>}
      />
      <div className="px-8 pb-10">
        <Section title="All collectors" flush>
          {collectors.length === 0 ? (
            <Empty
              icon={<ActivityIcon />}
              title={hasConnections ? 'No collectors yet' : 'Connect Prometheus and Flexprice first'}
              description={hasConnections ? 'Create one to turn a PromQL query into daily usage events.' : 'A collector reads from a Prometheus connection and sends to a Flexprice connection.'}
              action={hasConnections ? null : <Button variant="outline" onClick={() => go('#/connections')}>Go to connections</Button>}
            />
          ) : (
            <Table head={['Collector', 'Event', 'Customer from', 'Schedule', 'Checks', 'Version', 'Updated']}>
              {collectors.map((c) => (
                <Tr key={c.id} onClick={() => go(`#/collectors/${c.id}`)}>
                  <Td>
                    <p className="font-medium">{c.name || c.id}</p>
                    <p className="text-muted-foreground font-mono text-xs">{c.id}</p>
                  </Td>
                  <Td mono>{c.mapping.event_name}</Td>
                  <Td>
                    {c.mapping.customer.from === 'static'
                      ? <span className="font-mono text-xs">{c.mapping.customer.value}</span>
                      : <span className="text-xs">label <span className="font-mono">{c.mapping.customer.label}</span>{Object.keys(c.mapping.customer.lookup ?? {}).length ? ` · ${Object.keys(c.mapping.customer.lookup ?? {}).length} mapped` : ''}</span>}
                  </Td>
                  <Td mono>{c.trigger.schedule} UTC</Td>
                  <Td>{c.trigger.completeness ? <Badge tone="success" dot>Completeness gate</Badge> : <Badge>No gate</Badge>}</Td>
                  <Td><Badge tone="info">v{c.version}</Badge></Td>
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
