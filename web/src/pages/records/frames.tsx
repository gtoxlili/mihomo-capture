import { lazy, Suspense, useState } from "react"
import { ArrowDownLeftIcon, ArrowUpRightIcon } from "lucide-react"

import type { Frame } from "@/lib/api"
import { opcodeName, prettyJSON, viewBody } from "@/lib/body"
import { formatBytes, formatClock } from "@/lib/format"
import { cn } from "@/lib/utils"
import { HexView } from "./hex-view"

const CodeView = lazy(() => import("./code-view"))

export function Frames({ frames, truncated }: { frames: Frame[]; truncated: boolean }) {
  const [open, setOpen] = useState<number | null>(null)
  if (frames.length === 0) return <p className="text-[13px] text-muted-foreground">连接建立了，还没有帧。</p>
  return (
    <div className="space-y-3">
      <p className="text-xs text-muted-foreground">
        <ArrowUpRightIcon className="inline size-3.5 text-signal" /> 设备发出
        <ArrowDownLeftIcon className="ml-3 inline size-3.5 text-good" /> 服务器发来
        {truncated && <span className="ml-3">只保留了前 {frames.length} 帧，之后的帧照常转发但不记录。</span>}
      </p>
      <ol className="overflow-hidden rounded-md border">
        {frames.map((f, i) => {
          const out = f.direction === "client_to_server"
          const text = f.body.encoding === "utf8" ? f.body.data : ""
          return (
            <li key={i} className="border-b last:border-b-0">
              <button
                type="button"
                onClick={() => setOpen(open === i ? null : i)}
                aria-expanded={open === i}
                className={cn(
                  "grid w-full grid-cols-[1rem_6.5rem_3.5rem_4rem_minmax(0,1fr)] items-center gap-x-3 px-3 py-1.5 text-left font-mono text-xs hover:bg-muted/70",
                  open === i && "bg-accent"
                )}
              >
                {out ? (
                  <ArrowUpRightIcon className="size-3.5 text-signal" aria-label="设备发出" />
                ) : (
                  <ArrowDownLeftIcon className="size-3.5 text-good" aria-label="服务器发来" />
                )}
                <span className="text-muted-foreground tabular">{formatClock(f.at)}</span>
                <span className="font-sans">{opcodeName[f.opcode] ?? `op ${f.opcode}`}</span>
                <span className="text-right text-muted-foreground tabular">{formatBytes(f.body.size)}</span>
                <span className="truncate text-muted-foreground">
                  {f.compressed ? "压缩载荷，展开看原始字节" : text || (f.body.size ? "二进制" : "")}
                  {!f.final && <span className="ml-2 font-sans">未完，后接续帧</span>}
                </span>
              </button>
              {open === i && (
                <div className="border-t bg-card p-3">
                  <FramePayload frame={f} />
                </div>
              )}
            </li>
          )
        })}
      </ol>
    </div>
  )
}

function FramePayload({ frame }: { frame: Frame }) {
  const view = viewBody(frame.body, "")
  if (view.kind === "empty") return <p className="text-[13px] text-muted-foreground">空载荷</p>
  if (view.kind === "text")
    return (
      <Suspense fallback={null}>
        <CodeView text={view.language === "json" ? prettyJSON(view.text) : view.text} language={view.language} />
      </Suspense>
    )
  if (view.kind === "binary") return <HexView bytes={view.bytes} />
  return null
}
