import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react'
import { ActivityIcon, CableIcon, KeyRoundIcon, LayersIcon } from 'lucide-react'
import { api, cn, setToken, Unauthorized, type Collector, type Connection } from '@/lib'
import { Button, Field, Input } from '@/components/ui'
import { CollectorsPage } from '@/pages/Collectors'
import { ConnectionsPage } from '@/pages/Connections'
import { CollectorEditor } from '@/pages/Editor'

interface Ctx {
  connections: Connection[]
  collectors: Collector[]
  reload: () => Promise<void>
  toast: (msg: string, error?: boolean) => void
  go: (hash: string) => void
}
const AppCtx = createContext<Ctx>(null!)
export const useApp = () => useContext(AppCtx)

function useHash() {
  const [hash, setHash] = useState(location.hash || '#/collectors')
  useEffect(() => {
    const on = () => setHash(location.hash || '#/collectors')
    window.addEventListener('hashchange', on)
    return () => window.removeEventListener('hashchange', on)
  }, [])
  return hash
}

export function App() {
  const hash = useHash()
  const [connections, setConnections] = useState<Connection[]>([])
  const [collectors, setCollectors] = useState<Collector[]>([])
  const [needToken, setNeedToken] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [toastMsg, setToastMsg] = useState<{ msg: string; error?: boolean } | null>(null)

  const toast = useCallback((msg: string, error?: boolean) => {
    setToastMsg({ msg, error })
    window.setTimeout(() => setToastMsg(null), error ? 7000 : 3000)
  }, [])

  const reload = useCallback(async () => {
    try {
      const [cs, cols] = await Promise.all([api<Connection[]>('GET', '/api/connections'), api<Collector[]>('GET', '/api/collectors')])
      setConnections(cs)
      setCollectors(cols)
      setNeedToken(false)
    } catch (e) {
      if (e instanceof Unauthorized) setNeedToken(true)
      else toast((e as Error).message, true)
    } finally {
      setLoaded(true)
    }
  }, [toast])

  useEffect(() => { void reload() }, [reload])

  const go = (h: string) => { location.hash = h }
  const [, page, id] = hash.split('/')

  if (needToken) return <TokenGate onSubmit={(t) => { setToken(t); void reload() }} />

  let body: ReactNode = null
  if (loaded) {
    if (page === 'connections') body = <ConnectionsPage />
    else if (page === 'collectors' && id) body = <CollectorEditor key={id} id={id === 'new' ? null : id} />
    else body = <CollectorsPage />
  }

  return (
    <AppCtx.Provider value={{ connections, collectors, reload, toast, go }}>
      <div className="flex min-h-dvh">
        <aside className="bg-sidebar sticky top-0 flex h-dvh w-60 shrink-0 flex-col border-r">
          <div className="flex h-14 items-center gap-2 border-b px-4">
            <div className="bg-primary text-primary-foreground flex size-7 items-center justify-center rounded-lg"><LayersIcon className="size-4" /></div>
            <div className="leading-tight">
              <p className="text-sm font-medium">flexprice</p>
              <p className="text-muted-foreground text-[11px]">Usage collector</p>
            </div>
          </div>
          <nav className="flex flex-col gap-0.5 p-2">
            <NavItem icon={<ActivityIcon />} label="Collectors" active={page !== 'connections'} href="#/collectors" count={collectors.length} />
            <NavItem icon={<CableIcon />} label="Connections" active={page === 'connections'} href="#/connections" count={connections.length} />
          </nav>
          <div className="mt-auto border-t p-3">
            <div className="text-muted-foreground flex items-center gap-2 text-xs">
              <span className="bg-success size-1.5 rounded-full" /> Self-hosted control plane
            </div>
          </div>
        </aside>
        <main className="bg-muted/30 min-w-0 flex-1">{body}</main>
      </div>
      {toastMsg ? (
        <div role="status" className={cn('fixed right-5 bottom-5 z-50 max-w-md rounded-lg px-4 py-2.5 text-sm shadow-lg', toastMsg.error ? 'bg-destructive text-white' : 'bg-foreground text-background')}>
          {toastMsg.msg}
        </div>
      ) : null}
    </AppCtx.Provider>
  )
}

function NavItem({ icon, label, active, href, count }: { icon: ReactNode; label: string; active: boolean; href: string; count: number }) {
  return (
    <a href={href} className={cn('flex h-8 items-center gap-2 rounded-lg px-2.5 text-sm [&_svg]:size-4', active ? 'bg-background font-medium shadow-xs ring-1 ring-border' : 'text-muted-foreground hover:bg-muted hover:text-foreground')}>
      {icon}
      <span className="flex-1">{label}</span>
      <span className="text-muted-foreground text-xs tabular-nums">{count}</span>
    </a>
  )
}

function TokenGate({ onSubmit }: { onSubmit: (t: string) => void }) {
  const [t, setT] = useState('')
  return (
    <div className="bg-muted/30 flex min-h-dvh items-center justify-center p-6">
      <form className="bg-background flex w-full max-w-sm flex-col gap-4 rounded-xl border p-6 shadow-xs" onSubmit={(e) => { e.preventDefault(); onSubmit(t.trim()) }}>
        <div className="bg-primary text-primary-foreground flex size-9 items-center justify-center rounded-lg"><KeyRoundIcon className="size-4" /></div>
        <div>
          <h1 className="text-lg font-medium">Sign in to the collector</h1>
          <p className="text-muted-foreground text-[13px]">Enter the collector token set as COLLECTOR_UI_TOKEN on this deployment.</p>
        </div>
        <Field label="Collector token"><Input type="password" autoFocus value={t} onChange={(e) => setT(e.target.value)} /></Field>
        <Button type="submit" disabled={!t.trim()}>Continue</Button>
      </form>
    </div>
  )
}
