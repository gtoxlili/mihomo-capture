import { Link, useLocation, useNavigate } from "react-router"
import { SquareIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import type { Capture, Device, RecordSummary } from "@/lib/api"
import { deviceName, formatClock, formatDuration, formatElapsed, statusTone, type StatusTone } from "@/lib/format"
import { useNow } from "@/lib/hooks"
import { useDevices, useStatus, useStopCapture, useTicker } from "@/lib/queries"
import { cn } from "@/lib/utils"

// The band under the navigation says whose traffic is being recorded. It is
// the one loud element of the console: quiet in daily mode, on air otherwise.
export function TapBand() {
  const status = useStatus()
  const devices = useDevices()
  const capture = status.data?.capture
  if (!status.data) return <div className="h-16 shrink-0 border-b bg-card" />
  if (!capture) {
    const listening = devices.data?.filter((d) => d.trusted && d.online).length ?? 0
    return <IdleBand listening={listening} />
  }
  return <LiveBand capture={capture} device={devices.data?.find((d) => d.id === capture.device_id)} />
}

function IdleBand({ listening }: { listening: number }) {
  return (
    <section className="flex min-h-16 shrink-0 flex-wrap items-center gap-x-4 gap-y-2 border-b bg-card px-5 py-3">
      <span aria-hidden className="size-2.5 shrink-0 rounded-full ring-[1.5px] ring-faint ring-inset" />
      <div className="min-w-0 flex-1">
        <p className="font-semibold">日常模式</p>
        <p className="text-[13px] text-muted-foreground">
          没有设备在被抓包。
          {listening > 0
            ? `${listening} 台受信设备在线，只在访问模块 hostname 时被解密，不产生记录。`
            : "受信设备只在访问模块 hostname 时被解密，不产生记录。"}
        </p>
      </div>
      <Button asChild variant="outline">
        <Link to="/devices">选择设备抓包</Link>
      </Button>
    </section>
  )
}

function LiveBand({ capture, device }: { capture: Capture; device?: Device }) {
  const now = useNow()
  const stop = useStopCapture()
  const location = useLocation()
  const ticker = useTicker(capture.device_id)
  const recent = (ticker.data ?? []).filter((r) => r.session === capture.generation)
  const elapsed = now - new Date(capture.started_at).getTime()
  return (
    <section
      aria-label="正在抓包"
      className="relative flex min-h-[4.5rem] shrink-0 flex-wrap items-center gap-x-6 gap-y-3 border-b border-tally-line bg-tally-wash px-5 py-3 before:absolute before:inset-x-0 before:top-0 before:h-0.5 before:bg-tally"
    >
      <div className="flex min-w-0 items-center gap-3">
        <span aria-hidden className="size-2.5 shrink-0 animate-tally rounded-full bg-tally" />
        <div className="min-w-0">
          <p className="text-xs font-medium text-tally-ink">正在抓包</p>
          <p className="truncate text-[17px] leading-tight font-semibold tracking-tight">{deviceName(device)}</p>
        </div>
      </div>
      <dl className="flex items-stretch text-[13px] [&>div]:border-l [&>div]:border-tally-line [&>div]:px-5">
        <div>
          <dt className="text-xs text-muted-foreground">已持续</dt>
          <dd className="font-mono text-[17px] leading-tight font-medium tabular">{formatElapsed(elapsed)}</dd>
        </div>
        <div className="hidden sm:block">
          <dt className="text-xs text-muted-foreground">IP</dt>
          <dd className="pt-0.5 font-mono leading-tight text-ink-2">{device?.ips.join(", ") || "—"}</dd>
        </div>
        <div className="hidden md:block">
          <dt className="text-xs text-muted-foreground">QUIC</dt>
          <dd className="pt-0.5 leading-tight text-ink-2">
            {capture.block_udp_ports?.length ? `阻断 UDP ${capture.block_udp_ports.join(", ")}` : "未阻断"}
          </dd>
        </div>
      </dl>
      <Ticker records={recent} />
      <div className="ml-auto flex items-center gap-2">
        {location.pathname !== "/" && (
          <Button asChild variant="ghost" className="text-tally-ink hover:bg-tally/10 hover:text-tally-ink">
            <Link to={`/?device=${capture.device_id}`}>查看记录</Link>
          </Button>
        )}
        <Button variant="danger" disabled={stop.isPending} onClick={() => stop.mutate([])}>
          <SquareIcon className="size-3 fill-current" />
          停止抓包
        </Button>
      </div>
    </section>
  )
}

const slots = 48

const markFill: Record<StatusTone, string> = {
  ok: "bg-mark-ok",
  redirect: "bg-mark-redirect",
  client: "bg-mark-client",
  server: "bg-mark-server",
  pending: "bg-faint",
}

const legend: [StatusTone, string][] = [
  ["ok", "2xx"],
  ["redirect", "3xx"],
  ["client", "4xx"],
  ["server", "5xx 或失败"],
]

// The latest requests of this capture, oldest on the left. A bar's colour is
// its status class and its height its duration on a log scale.
function Ticker({ records }: { records: RecordSummary[] }) {
  const navigate = useNavigate()
  const items = records.slice(0, slots).reverse()
  const pad = slots - items.length
  return (
    <div className="hidden min-w-0 flex-1 basis-60 flex-col gap-1 lg:flex">
      <div className="flex h-8 items-end" role="list" aria-label={`本次抓包最近 ${items.length} 个请求`}>
        {Array.from({ length: pad }, (_, i) => (
          <span key={`pad-${i}`} className="flex h-full w-[9px] shrink-0 items-end px-[1.5px]">
            <span className="h-1 w-full rounded-[1px] bg-ink/8" />
          </span>
        ))}
        {items.map((r) => {
          const h = r.complete ? Math.min(1, Math.max(0.18, Math.log10(r.duration_ms + 10) / 4)) : 0.5
          const tone = r.error && !r.status ? "server" : statusTone(r.status)
          return (
            <button
              key={r.id}
              role="listitem"
              type="button"
              title={`${r.method} ${r.status || (r.error ? "失败" : "进行中")}  ${r.host}${r.path}\n${formatDuration(r.duration_ms)}  ${formatClock(r.at)}`}
              onClick={() => navigate(`/?device=${r.device_id}&r=${r.id}`)}
              className="group flex h-full w-[9px] shrink-0 items-end px-[1.5px] focus-visible:outline-2 focus-visible:outline-signal"
            >
              <span
                style={{ height: `${h * 100}%` }}
                className={cn(
                  "w-full rounded-[1px] transition-[height] group-hover:opacity-70",
                  r.complete ? markFill[tone] : "border border-faint"
                )}
              />
            </button>
          )
        })}
      </div>
      {items.length ? (
        <p className="flex flex-wrap items-center gap-x-3 text-xs text-muted-foreground">
          {legend.map(([tone, label]) => (
            <span key={tone} className="flex items-center gap-1">
              <span className={cn("size-2 rounded-[1px]", markFill[tone])} />
              {label}
            </span>
          ))}
          <span>高度是耗时</span>
        </p>
      ) : (
        <p className="text-xs text-muted-foreground">
          还没有新请求。设备上已经建立的连接继续走原路，重新打开 App 或刷新页面后才会出现。
        </p>
      )}
    </div>
  )
}
