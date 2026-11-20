import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react'
import { ActivityIcon, CableIcon, LayersIcon, PanelLeftCloseIcon, PanelLeftOpenIcon } from 'lucide-react'
import { api, cn, type Collector, type Connection } from '@/lib'
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
  const [loaded, setLoaded] = useState(false)
  const [toastMsg, setToastMsg] = useState<{ msg: string; error?: boolean } | null>(null)
  const [collapsed, setCollapsed] = useState(() => { try { return localStorage.getItem('sidebar') === 'collapsed' } catch { return false } })
  const toggleSidebar = () => setCollapsed((c) => { try { localStorage.setItem('sidebar', c ? 'open' : 'collapsed') } catch { /* private mode */ } return !c })

  const toast = useCallback((msg: string, error?: boolean) => {
    setToastMsg({ msg, error })
    window.setTimeout(() => setToastMsg(null), error ? 7000 : 3000)
  }, [])

  const reload = useCallback(async () => {
    try {
      const [cs, cols] = await Promise.all([api<Connection[]>('GET', '/api/connections'), api<Collector[]>('GET', '/api/collectors')])
      setConnections(cs)
      setCollectors(cols)
    } catch (e) {
      toast((e as Error).message, true)
    } finally {
      setLoaded(true)
    }
  }, [toast])

  useEffect(() => { void reload() }, [reload])

  const go = (h: string) => { location.hash = h }
  const [, page, id] = hash.split('/')

  let body: ReactNode = null
  if (loaded) {
    if (page === 'connections') body = <ConnectionsPage />
    else if (page === 'collectors' && id) body = <CollectorEditor key={id} id={id === 'new' ? null : id} />
    else body = <CollectorsPage />
  }

  return (
    <AppCtx.Provider value={{ connections, collectors, reload, toast, go }}>
      <div className="flex min-h-dvh">
        <aside className={cn('bg-sidebar sticky top-0 flex h-dvh shrink-0 flex-col border-r transition-[width] duration-200', collapsed ? 'w-14' : 'w-60')}>
          <div className={cn('flex h-14 items-center gap-2 border-b', collapsed ? 'justify-center px-2' : 'px-4')}>
            <div className="bg-primary text-primary-foreground flex size-7 shrink-0 items-center justify-center rounded-lg"><LayersIcon className="size-4" /></div>
            {collapsed ? null : (
              <div className="leading-tight">
                <p className="text-sm font-medium">flexprice</p>
                <p className="text-muted-foreground text-[11px]">Usage collector</p>
              </div>
            )}
          </div>
          <nav className="flex flex-col gap-0.5 p-2">
            <NavItem icon={<ActivityIcon />} label="Collectors" active={page !== 'connections'} href="#/collectors" count={collectors.length} collapsed={collapsed} />
            <NavItem icon={<CableIcon />} label="Connections" active={page === 'connections'} href="#/connections" count={connections.length} collapsed={collapsed} />
          </nav>
          <div className={cn('mt-auto flex items-center border-t p-2', collapsed ? 'justify-center' : 'justify-between pl-3')}>
            {collapsed ? null : <span className="text-muted-foreground flex items-center gap-2 text-xs"><span className="bg-success size-1.5 rounded-full" /> Self-hosted</span>}
            <button type="button" onClick={toggleSidebar} aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'} title={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
              className="text-muted-foreground hover:bg-muted hover:text-foreground flex size-8 items-center justify-center rounded-lg [&_svg]:size-4">
              {collapsed ? <PanelLeftOpenIcon aria-hidden /> : <PanelLeftCloseIcon aria-hidden />}
            </button>
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

function NavItem({ icon, label, active, href, count, collapsed }: { icon: ReactNode; label: string; active: boolean; href: string; count: number; collapsed: boolean }) {
  return (
    <a href={href} title={collapsed ? label : undefined} aria-label={collapsed ? label : undefined}
      className={cn('flex h-8 items-center gap-2 rounded-lg text-sm [&_svg]:size-4', collapsed ? 'justify-center' : 'px-2.5', active ? 'bg-background font-medium shadow-xs ring-1 ring-border' : 'text-muted-foreground hover:bg-muted hover:text-foreground')}>
      {icon}
      {collapsed ? null : <><span className="flex-1">{label}</span><span className="text-muted-foreground text-xs tabular-nums">{count}</span></>}
    </a>
  )
}
