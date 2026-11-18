import { forwardRef, type InputHTMLAttributes, type ReactNode, type SelectHTMLAttributes, type TextareaHTMLAttributes } from 'react'
import { CheckCircle2Icon, CircleAlertIcon, InfoIcon, TriangleAlertIcon } from 'lucide-react'
import { cn } from '@/lib'
import { Input as PInput } from '@/components/primitives/input'
import { Textarea as PTextarea } from '@/components/primitives/textarea'
import { Switch as PSwitch } from '@/components/primitives/switch'
import { Table as PTable, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/primitives/table'
import { Badge as ReuiBadge } from '@/components/reui/badge'
import { Alert as ReuiAlert, AlertDescription, AlertTitle } from '@/components/reui/alert'

// Mirrors the Flexprice console kit (portal packages/ui): shadcn base-nova sizes, tokens only.
export { Button } from '@/components/primitives/button'

const control = 'w-full min-w-0 rounded-lg border border-input bg-background px-2.5 text-sm transition-colors outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:bg-muted disabled:opacity-60 aria-invalid:border-destructive'
export const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(({ className, ...p }, ref) => <PInput ref={ref} className={className} {...p} />)
export const Textarea = forwardRef<HTMLTextAreaElement, TextareaHTMLAttributes<HTMLTextAreaElement>>(({ className, ...p }, ref) => (
  <PTextarea ref={ref} className={cn('min-h-20 font-mono text-[12.5px] leading-5', className)} {...p} />
))
export function Select({ className, options, ...p }: SelectHTMLAttributes<HTMLSelectElement> & { options: [string, string][] }) {
  return (
    <select className={cn(control, 'h-8 appearance-none bg-[length:16px] bg-[right_8px_center] bg-no-repeat pr-8', 'bg-[url("data:image/svg+xml,%3Csvg xmlns=%27http://www.w3.org/2000/svg%27 viewBox=%270 0 24 24%27 fill=%27none%27 stroke=%27%2371717a%27 stroke-width=%272%27%3E%3Cpath d=%27m6 9 6 6 6-6%27/%3E%3C/svg%3E")]', className)} {...p}>
      {options.map(([v, l]) => <option key={v} value={v}>{l}</option>)}
    </select>
  )
}

export function Field({ label, hint, children, className, error }: { label: string; hint?: ReactNode; children: ReactNode; className?: string; error?: string }) {
  return (
    <label className={cn('flex flex-col gap-1.5', className)}>
      <span className="text-[13px] font-medium">{label}</span>
      {children}
      {error ? <span className="text-destructive text-xs">{error}</span> : hint ? <span className="text-muted-foreground text-xs leading-4">{hint}</span> : null}
    </label>
  )
}

const toneVariant = { neutral: 'secondary', success: 'success', warning: 'warning', error: 'destructive', info: 'info', primary: 'default' } as const
export function Badge({ tone = 'neutral', children, className, dot }: { tone?: keyof typeof toneVariant; children: ReactNode; className?: string; dot?: boolean }) {
  return (
    <ReuiBadge variant={toneVariant[tone]} className={className}>
      {dot ? <span className="size-1.5 rounded-full bg-current opacity-80" /> : null}
      {children}
    </ReuiBadge>
  )
}

export function PageHeader({ title, meta, actions, breadcrumb }: { title: ReactNode; meta?: ReactNode; actions?: ReactNode; breadcrumb?: ReactNode }) {
  return (
    <header className="flex flex-col gap-2 px-8 pt-6 pb-5">
      {breadcrumb}
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="flex min-w-0 flex-col gap-1">
          <h1 className="truncate text-xl font-medium tracking-tight">{title}</h1>
          {meta ? <div className="text-muted-foreground text-[13px]">{meta}</div> : null}
        </div>
        {actions ? <div className="flex shrink-0 items-center gap-2">{actions}</div> : null}
      </div>
    </header>
  )
}

export function Section({ title, description, actions, children, flush, className }: { title: string; description?: ReactNode; actions?: ReactNode; children: ReactNode; flush?: boolean; className?: string }) {
  return (
    <section className={cn('bg-background flex flex-col overflow-hidden rounded-xl border', className)}>
      <div className="flex items-center justify-between gap-4 border-b px-5 py-3.5">
        <div className="min-w-0">
          <h2 className="text-sm font-medium">{title}</h2>
          {description ? <p className="text-muted-foreground mt-0.5 text-[13px]">{description}</p> : null}
        </div>
        {actions ? <div className="-my-1 flex shrink-0 items-center gap-2">{actions}</div> : null}
      </div>
      <div className={cn('min-w-0 flex-1', flush ? '' : 'p-5')}>{children}</div>
    </section>
  )
}

export function Empty({ icon, title, description, action }: { icon?: ReactNode; title: string; description?: ReactNode; action?: ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center gap-2 px-6 py-12 text-center">
      {icon ? <div className="bg-muted text-muted-foreground mb-1 flex size-10 items-center justify-center rounded-lg [&_svg]:size-5">{icon}</div> : null}
      <p className="text-sm font-medium">{title}</p>
      {description ? <p className="text-muted-foreground max-w-sm text-[13px]">{description}</p> : null}
      {action ? <div className="mt-2">{action}</div> : null}
    </div>
  )
}

export function Table({ head, children, className }: { head: ReactNode[]; children: ReactNode; className?: string }) {
  return (
    <PTable className={className}>
      <TableHeader><TableRow>{head.map((h, i) => <TableHead key={i} className="px-4">{h}</TableHead>)}</TableRow></TableHeader>
      <TableBody>{children}</TableBody>
    </PTable>
  )
}
export const Tr = ({ children, onClick, className }: { children: ReactNode; onClick?: () => void; className?: string }) => (
  <TableRow onClick={onClick} className={cn(onClick && 'cursor-pointer', className)}>{children}</TableRow>
)
export const Td = ({ children, className, mono }: { children?: ReactNode; className?: string; mono?: boolean }) => (
  <TableCell className={cn('px-4 py-2.5 align-top whitespace-normal', mono && 'font-mono text-xs', className)}>{children}</TableCell>
)

const alertIcon = { ok: CheckCircle2Icon, warning: TriangleAlertIcon, error: CircleAlertIcon, info: InfoIcon }
const alertVariant = { ok: 'success', warning: 'warning', error: 'destructive', info: 'info' } as const
export function Alert({ level, title, children, className }: { level: keyof typeof alertIcon; title?: ReactNode; children?: ReactNode; className?: string }) {
  const Icon = alertIcon[level]
  return (
    <ReuiAlert variant={alertVariant[level]} className={className}>
      <Icon aria-hidden />
      {title ? <AlertTitle className="line-clamp-none">{title}</AlertTitle> : null}
      {children ? (title ? <AlertDescription>{children}</AlertDescription> : <AlertTitle className="line-clamp-none font-normal tracking-normal">{children}</AlertTitle>) : null}
    </ReuiAlert>
  )
}

export function Stat({ label, value, tone, hint }: { label: string; value: ReactNode; tone?: 'success' | 'error' | 'warning'; hint?: ReactNode }) {
  return (
    <div className="bg-background flex flex-col gap-1 rounded-xl border px-4 py-3">
      <span className="text-muted-foreground text-xs">{label}</span>
      <span className={cn('truncate text-xl font-medium tracking-tight tabular-nums', tone === 'success' && 'text-success', tone === 'error' && 'text-destructive', tone === 'warning' && 'text-warning-foreground')}>{value}</span>
      {hint ? <span className="text-muted-foreground text-xs">{hint}</span> : null}
    </div>
  )
}

export function Code({ children, className }: { children: ReactNode; className?: string }) {
  return <pre className={cn('bg-muted/50 max-h-[520px] overflow-auto rounded-lg border p-3 font-mono text-xs leading-5', className)}>{children}</pre>
}

export function Spinner({ className }: { className?: string }) {
  return <span className={cn('inline-block size-3.5 animate-spin rounded-full border-2 border-current border-r-transparent', className)} aria-hidden />
}

export function Switch({ checked, onChange, label }: { checked: boolean; onChange: (v: boolean) => void; label: string }) {
  return <PSwitch aria-label={label} checked={checked} onCheckedChange={(v: boolean) => onChange(v)} />
}
