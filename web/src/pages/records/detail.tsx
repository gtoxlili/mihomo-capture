import { CopyIcon, FilterIcon, TriangleAlertIcon, XIcon } from "lucide-react"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { ApiError, type CaptureRecord, type Device } from "@/lib/api"
import { header, headerEntries } from "@/lib/body"
import { deviceName, formatBytes, formatDateTime, formatDuration, statusTone, toneText } from "@/lib/format"
import { copyText } from "@/lib/hooks"
import { errorMessage, useRecord } from "@/lib/queries"
import { cn } from "@/lib/utils"
import { BodyPanel } from "./body"
import { Frames } from "./frames"
import { KeyValues } from "./key-values"

type Props = {
  id: string
  devices: Device[]
  onClose: () => void
  onFilter: (patch: Record<string, string>) => void
}

export function RecordDetail({ id, devices, onClose, onFilter }: Props) {
  const q = useRecord(id)
  return (
    <div className="flex h-full flex-col bg-card">
      {q.data ? (
        <Loaded r={q.data} devices={devices} onClose={onClose} onFilter={onFilter} />
      ) : (
        <div className="flex h-full flex-col">
          <div className="flex h-11 items-center justify-end border-b px-3">
            <CloseButton onClose={onClose} />
          </div>
          <p className={cn("p-6 text-[13px] text-muted-foreground", q.error && "text-destructive")}>
            {q.error instanceof ApiError && q.error.status === 404
              ? "这条记录已经不在了：它可能被清空，或者超出了保留期限和容量后被删除。"
              : q.error
                ? `读取记录失败：${errorMessage(q.error)}`
                : "正在读取记录…"}
          </p>
        </div>
      )}
    </div>
  )
}

function CloseButton({ onClose }: { onClose: () => void }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button variant="ghost" size="icon-sm" onClick={onClose} aria-label="关闭详情">
          <XIcon />
        </Button>
      </TooltipTrigger>
      <TooltipContent>关闭（Esc）</TooltipContent>
    </Tooltip>
  )
}

function Loaded({ r, devices, onClose, onFilter }: { r: CaptureRecord } & Omit<Props, "id">) {
  const device = devices.find((d) => d.id === r.device_id)
  const tone = statusTone(r.status)
  const query = queryParams(r.url)
  const frames = r.frames ?? []
  const requestType = header(r.request.headers, "Content-Type")
  const responseType = header(r.response.headers, "Content-Type")
  return (
    <div
      className="flex h-full flex-col"
      onKeyDown={(e) => {
        if (e.key === "Escape" && !(e.target instanceof HTMLElement && e.target.closest(".cm-editor"))) onClose()
      }}
    >
      <div className="shrink-0 space-y-3 border-b px-5 pt-3.5 pb-4">
        <div className="flex items-center gap-2">
          <span className="rounded bg-secondary px-1.5 py-0.5 font-mono text-xs font-semibold text-ink-2">
            {r.method}
          </span>
          <span
            className={cn(
              "font-mono text-[13px] font-semibold",
              r.error && !r.status ? toneText.server : toneText[tone]
            )}
          >
            {r.status ? `${r.status} ${reason[r.status] ?? ""}` : r.error ? "没有响应" : "等待响应"}
          </span>
          <div className="ml-auto flex items-center gap-1">
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label="只看这个 host"
                  onClick={() => onFilter({ host: r.host })}
                >
                  <FilterIcon />
                </Button>
              </TooltipTrigger>
              <TooltipContent>只看 {r.host}</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label="复制 URL"
                  onClick={() => copyText(r.url).then(() => toast.success("已复制 URL"))}
                >
                  <CopyIcon />
                </Button>
              </TooltipTrigger>
              <TooltipContent>复制 URL</TooltipContent>
            </Tooltip>
            <CloseButton onClose={onClose} />
          </div>
        </div>
        <p className="font-mono text-[13px] leading-relaxed break-all text-ink select-text">{r.url}</p>
        <dl className="grid grid-cols-2 gap-x-6 gap-y-2 text-[13px] sm:grid-cols-3 xl:grid-cols-6">
          <Fact label="耗时">{r.complete ? formatDuration(r.duration_ms) : "进行中"}</Fact>
          <Fact label="响应正文">{r.complete ? formatBytes(r.response.body.size) : "—"}</Fact>
          <Fact label="开始于" wide>
            {formatDateTime(r.at)}
          </Fact>
          <Fact label="设备">{deviceName(device)}</Fact>
          <Fact label="会话">{r.session}</Fact>
        </dl>
        {r.modules && r.modules.length > 0 && (
          <p className="text-[13px] text-muted-foreground">
            经模块处理：<span className="text-ink">{r.modules.join("、")}</span>
            。这里是设备一侧看到的请求和响应。
          </p>
        )}
        {(r.error || !r.complete || !r.replayable) && (
          <div className="space-y-1.5">
            {r.error && <Flag tone="error">{r.error}</Flag>}
            {!r.complete && (
              <Flag tone="info">
                {r.status === 101 ? "WebSocket 还开着，新帧每隔几秒刷新进来。" : "响应还在传输，传完后这里会更新。"}
              </Flag>
            )}
            {r.complete && !r.replayable && <Flag tone="info">请求正文没有完整记下，这条请求不能生成 curl。</Flag>}
          </div>
        )}
      </div>
      <Tabs defaultValue={r.status === 101 ? "frames" : "response"} className="min-h-0 flex-1 gap-0">
        <TabsList variant="line" className="h-10 w-full shrink-0 justify-start gap-5 rounded-none border-b p-0 px-5">
          <TabsTrigger value="request" className={tab}>
            请求
          </TabsTrigger>
          <TabsTrigger value="response" className={tab}>
            响应
          </TabsTrigger>
          {(r.status === 101 || frames.length > 0) && (
            <TabsTrigger value="frames" className={tab}>
              帧 <span className="font-mono text-xs text-muted-foreground">{frames.length}</span>
            </TabsTrigger>
          )}
          <TabsTrigger value="curl" className={tab} disabled={!r.curl}>
            curl
          </TabsTrigger>
        </TabsList>
        <div className="min-h-0 flex-1 overflow-auto">
          <TabsContent value="request" className="space-y-6 p-5">
            {query.length > 0 && <KeyValues title="查询参数" entries={query} />}
            <KeyValues title="请求头" entries={headerEntries(r.request.headers)} />
            <BodyPanel title="请求正文" body={r.request.body} contentType={requestType} name={fileName(r, "request")} />
          </TabsContent>
          <TabsContent value="response" className="space-y-6 p-5">
            <KeyValues title="响应头" entries={headerEntries(r.response.headers)} />
            <BodyPanel
              title="响应正文"
              body={r.response.body}
              contentType={responseType}
              name={fileName(r, "response")}
              pending={!r.complete && r.status !== 101}
            />
            {r.response.trailers && Object.keys(r.response.trailers).length > 0 && (
              <KeyValues title="Trailer" entries={headerEntries(r.response.trailers)} />
            )}
          </TabsContent>
          <TabsContent value="frames" className="p-5">
            <Frames frames={frames} truncated={!!r.frames_truncated} />
          </TabsContent>
          <TabsContent value="curl" className="space-y-3 p-5">
            {r.curl && <Curl command={r.curl} />}
          </TabsContent>
        </div>
      </Tabs>
    </div>
  )
}

// Line tabs sit on the panel's rule; the active one is underlined in ink.
const tab =
  "h-full flex-none rounded-none px-0 text-[13px] group-data-horizontal/tabs:after:bottom-[-1px] data-active:font-semibold"

function Fact({ label, children, wide }: { label: string; children: React.ReactNode; wide?: boolean }) {
  return (
    <div className={cn("min-w-0", wide && "xl:col-span-2")}>
      <dt className="text-xs text-faint">{label}</dt>
      <dd className="truncate">{children}</dd>
    </div>
  )
}

function Flag({ tone, children }: { tone: "error" | "info"; children: React.ReactNode }) {
  return (
    <p
      className={cn(
        "flex items-start gap-2 rounded-md px-2.5 py-1.5 text-[13px]",
        tone === "error" ? "bg-destructive/8 text-destructive" : "bg-muted text-muted-foreground"
      )}
    >
      {tone === "error" && <TriangleAlertIcon className="mt-0.5 size-3.5 shrink-0" />}
      <span className="min-w-0 break-words">{children}</span>
    </p>
  )
}

function Curl({ command }: { command: string }) {
  return (
    <>
      <p className="flex items-start gap-2 rounded-md border border-warn-line bg-warn-wash px-3 py-2 text-[13px] text-warn-ink">
        <TriangleAlertIcon className="mt-0.5 size-4 shrink-0" />
        这条命令带着设备主人的 Cookie 和令牌。运行它就是以对方的身份再发一次真实请求，下单、发消息这类请求会再生效一次。
      </p>
      <div className="relative">
        <pre className="max-h-[60vh] overflow-auto rounded-md border bg-background p-3 pr-12 font-mono text-xs leading-relaxed break-all whitespace-pre-wrap select-text">
          {command}
        </pre>
        <Button
          variant="outline"
          size="sm"
          className="absolute top-2 right-2"
          onClick={() => copyText(command).then(() => toast.success("已复制 curl"))}
        >
          <CopyIcon />
          复制
        </Button>
      </div>
    </>
  )
}

function queryParams(url: string): [string, string][] {
  try {
    return [...new URL(url).searchParams]
  } catch {
    return []
  }
}

function fileName(r: CaptureRecord, part: string) {
  const last = r.path.split("?")[0].split("/").filter(Boolean).at(-1) || r.host
  return `${part}-${last}`
}

const reason: Record<number, string> = {
  100: "Continue",
  101: "Switching Protocols",
  200: "OK",
  201: "Created",
  202: "Accepted",
  204: "No Content",
  206: "Partial Content",
  301: "Moved Permanently",
  302: "Found",
  303: "See Other",
  304: "Not Modified",
  307: "Temporary Redirect",
  308: "Permanent Redirect",
  400: "Bad Request",
  401: "Unauthorized",
  403: "Forbidden",
  404: "Not Found",
  405: "Method Not Allowed",
  408: "Request Timeout",
  409: "Conflict",
  410: "Gone",
  413: "Content Too Large",
  415: "Unsupported Media Type",
  422: "Unprocessable Content",
  429: "Too Many Requests",
  499: "Client Closed Request",
  500: "Internal Server Error",
  502: "Bad Gateway",
  503: "Service Unavailable",
  504: "Gateway Timeout",
}
