import { useEffect, useRef } from "react"
import { Link } from "react-router"
import { useVirtualizer } from "@tanstack/react-virtual"
import { LoaderIcon, PuzzleIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import type { Capture, Device, RecordSummary } from "@/lib/api"
import { contentKind, deviceName, formatBytes, formatClock, formatDuration, statusTone, toneText } from "@/lib/format"
import { errorMessage } from "@/lib/queries"
import { cn } from "@/lib/utils"

type Row =
  { kind: "session"; key: string; device: string; session: number } | { kind: "record"; key: string; r: RecordSummary }

type Props = {
  items: RecordSummary[]
  devices: Device[]
  selected: string | null
  onSelect: (id: string) => void
  hasMore: boolean
  loadMore: () => void
  loading: boolean
  error: unknown
  filtered: boolean
  capture: Capture | null
  captureDevice?: Device
  onClearFilters: () => void
}

// Columns collapse with the list's own width, which the detail pane changes.
const grid =
  "grid grid-cols-[3.5rem_2.75rem_minmax(0,1fr)_4.5rem] @lg:grid-cols-[6.5rem_3.5rem_2.75rem_minmax(0,1fr)_4.5rem] @3xl:grid-cols-[6.5rem_3.5rem_2.75rem_minmax(0,1fr)_3rem_4.5rem_4.5rem] items-center gap-x-3"

const header = 28

export function RecordList(props: Props) {
  const { items, devices, selected, onSelect, hasMore, loadMore } = props
  const scroller = useRef<HTMLDivElement>(null)
  const names = new Map(devices.map((d) => [d.id, deviceName(d)]))

  // A divider opens each run of records from one capture session.
  const rows: Row[] = []
  let run = ""
  for (const r of items) {
    const next = `${r.device_id}#${r.session}`
    if (next !== run) rows.push({ kind: "session", key: `s-${r.id}`, device: r.device_id, session: r.session })
    run = next
    rows.push({ kind: "record", key: r.id, r })
  }

  // React Compiler leaves this component unmemoized: the virtualizer returns new functions each render.
  // eslint-disable-next-line react-hooks/incompatible-library
  const v = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scroller.current,
    estimateSize: (i) => (rows[i]?.kind === "session" ? 30 : 33),
    getItemKey: (i) => rows[i].key,
    overscan: 16,
    // Rows start below the sticky column header.
    scrollMargin: header,
    scrollPaddingStart: header,
  })
  const virtualItems = v.getVirtualItems()
  const last = virtualItems.at(-1)?.index ?? 0
  useEffect(() => {
    if (hasMore && last >= rows.length - 10) loadMore()
  }, [last, rows.length, hasMore, loadMore])

  const index = rows.findIndex((row) => row.kind === "record" && row.r.id === selected)
  function move(step: number) {
    for (let i = index + step; i >= 0 && i < rows.length; i += step) {
      const row = rows[i]
      if (row.kind === "record") {
        onSelect(row.r.id)
        v.scrollToIndex(i, { align: "auto" })
        return
      }
    }
  }

  if (props.loading) {
    return <Placeholder>正在读取记录…</Placeholder>
  }
  if (props.error) {
    return <Placeholder tone="error">读取记录失败：{errorMessage(props.error)}</Placeholder>
  }
  if (items.length === 0) return <Empty {...props} />

  return (
    <div
      ref={scroller}
      tabIndex={0}
      aria-label="请求记录，用方向键或 J、K 切换"
      onKeyDown={(e) => {
        if (e.key === "ArrowDown" || e.key === "j") {
          e.preventDefault()
          move(1)
        } else if (e.key === "ArrowUp" || e.key === "k") {
          e.preventDefault()
          move(-1)
        }
      }}
      className="@container h-full overflow-auto bg-card outline-none"
    >
      <div
        className={cn(
          grid,
          "sticky top-0 z-10 h-7 border-b bg-subtle/95 px-4 text-[11.5px] text-muted-foreground backdrop-blur"
        )}
      >
        <span className="hidden @lg:block">时间</span>
        <span>方法</span>
        <span>状态</span>
        <span>地址</span>
        <span className="hidden @3xl:block">类型</span>
        <span className="hidden text-right @3xl:block">大小</span>
        <span className="text-right">耗时</span>
      </div>
      <div style={{ height: v.getTotalSize(), position: "relative" }}>
        {virtualItems.map((item) => {
          const row = rows[item.index]
          return (
            <div
              key={item.key}
              data-index={item.index}
              ref={v.measureElement}
              className="absolute inset-x-0 top-0"
              style={{ transform: `translateY(${item.start - header}px)` }}
            >
              {row.kind === "session" ? (
                <div className="flex h-[30px] items-end gap-2 border-b bg-canvas px-4 pb-1.5 text-xs">
                  <span className="font-medium text-ink-2">{names.get(row.device) ?? row.device}</span>
                  <span className="text-faint">会话 {row.session}</span>
                </div>
              ) : (
                <RecordRow r={row.r} selected={row.r.id === selected} onSelect={onSelect} />
              )}
            </div>
          )
        })}
      </div>
      {hasMore && (
        <div className="flex h-10 items-center justify-center text-xs text-muted-foreground">
          <LoaderIcon className="mr-2 size-3.5 animate-spin" />
          正在载入更早的记录
        </div>
      )}
    </div>
  )
}

function RecordRow({ r, selected, onSelect }: { r: RecordSummary; selected: boolean; onSelect: (id: string) => void }) {
  const tone = statusTone(r.status)
  const failed = !!r.error && r.status === 0
  return (
    <button
      type="button"
      onClick={() => onSelect(r.id)}
      aria-current={selected || undefined}
      className={cn(
        grid,
        "relative h-[33px] w-full border-b border-line/70 px-4 text-left font-mono text-xs hover:bg-subtle",
        selected &&
          "bg-signal-wash before:absolute before:inset-y-0 before:left-0 before:w-[3px] before:bg-signal hover:bg-signal-wash"
      )}
    >
      <span className="hidden text-faint tabular @lg:block">{formatClock(r.at)}</span>
      <span className="truncate font-medium text-ink-2">{r.method}</span>
      <span
        className={cn(
          "tabular",
          failed ? toneText.server : toneText[tone],
          (failed || tone !== "ok") && "font-semibold"
        )}
      >
        {failed ? "ERR" : r.status || "…"}
      </span>
      <span className="min-w-0 truncate" title={r.modules ? `${r.url}\n经模块处理：${r.modules.join("、")}` : r.url}>
        {r.modules && <PuzzleIcon aria-label="经模块处理" className="mr-1 inline size-3 align-[-1px] text-signal" />}
        <span className="text-foreground">{r.host}</span>
        <span className="text-muted-foreground">{r.path}</span>
      </span>
      <span className="hidden text-faint @3xl:block">{contentKind(r.content_type, r.status)}</span>
      <span className="hidden text-right text-muted-foreground tabular @3xl:block">
        {r.status === 101 ? `${r.frames ?? 0} 帧` : r.complete ? formatBytes(r.response_size) : ""}
      </span>
      <span
        className={cn("text-right text-muted-foreground tabular", r.duration_ms >= 1000 && "font-medium text-ink-2")}
      >
        {r.complete ? formatDuration(r.duration_ms) : <span className="text-signal">进行中</span>}
      </span>
    </button>
  )
}

function Placeholder({ children, tone }: { children: React.ReactNode; tone?: "error" }) {
  return (
    <div
      className={cn(
        "flex h-full items-center justify-center bg-card p-8 text-[13px] text-muted-foreground",
        tone === "error" && "text-destructive"
      )}
    >
      {children}
    </div>
  )
}

function Empty({ filtered, capture, captureDevice, onClearFilters }: Props) {
  let title = "还没有记录"
  let body: React.ReactNode = "选一台已确认信任根证书的设备开始抓包，它新建立的 HTTP 和 HTTPS 请求会出现在这里。"
  let action: React.ReactNode = (
    <Button asChild>
      <Link to="/devices">选择设备</Link>
    </Button>
  )
  if (filtered) {
    title = "没有符合筛选的记录"
    body = "记录里混着以往每一次抓包，按设备筛选时只剩那台设备的。"
    action = (
      <Button variant="outline" onClick={onClearFilters}>
        清除筛选
      </Button>
    )
  } else if (capture) {
    title = `正在等待${deviceName(captureDevice)}的新连接`
    body =
      "抓包只对开始之后新建立的连接生效。在设备上彻底关掉再重新打开 App，或者刷新网页，请求就会出现。证书固定的 App 在抓包期间连不上，走 HTTP/3 的请求需要阻断 UDP 443 才会回落到 TCP。"
    action = null
  }
  return (
    <div className="flex h-full items-start justify-center bg-card px-6 pt-[12vh]">
      <div className="max-w-md space-y-3">
        <h2 className="text-base font-semibold">{title}</h2>
        <p className="text-[13px] leading-relaxed text-muted-foreground">{body}</p>
        {action}
      </div>
    </div>
  )
}
