import type { Device } from "./api"

export function formatBytes(n: number) {
  if (n < 1024) return `${n} B`
  const units = ["KiB", "MiB", "GiB"]
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[i]}`
}

export function formatDuration(ms: number) {
  if (ms < 1000) return `${ms} ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(ms < 10_000 ? 2 : 1)} s`
  const m = Math.floor(ms / 60_000)
  return `${m} min ${Math.round((ms % 60_000) / 1000)} s`
}

export function formatElapsed(ms: number) {
  const s = Math.max(0, Math.floor(ms / 1000))
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${pad(Math.floor(s / 3600))}:${pad(Math.floor((s % 3600) / 60))}:${pad(s % 60)}`
}

const clock = new Intl.DateTimeFormat("zh-CN", {
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
  hour12: false,
})

export function formatClock(iso: string) {
  const d = new Date(iso)
  return `${clock.format(d)}.${String(d.getMilliseconds()).padStart(3, "0")}`
}

const dateTime = new Intl.DateTimeFormat("zh-CN", {
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
  hour12: false,
})

export function formatDateTime(iso: string) {
  return dateTime.format(new Date(iso))
}

const relative = new Intl.RelativeTimeFormat("zh-CN", { numeric: "auto" })

export function formatRelative(iso: string, now = Date.now()) {
  const s = Math.round((new Date(iso).getTime() - now) / 1000)
  const a = Math.abs(s)
  if (a < 45) return relative.format(s, "second")
  if (a < 2700) return relative.format(Math.round(s / 60), "minute")
  if (a < 79200) return relative.format(Math.round(s / 3600), "hour")
  if (a < 2592000) return relative.format(Math.round(s / 86400), "day")
  return formatDateTime(iso)
}

export type StatusTone = "ok" | "redirect" | "client" | "server" | "pending"

export function statusTone(status: number): StatusTone {
  if (status >= 500) return "server"
  if (status >= 400) return "client"
  if (status >= 300) return "redirect"
  if (status >= 100) return "ok"
  return "pending"
}

export const toneText: Record<StatusTone, string> = {
  ok: "text-status-ok",
  redirect: "text-status-redirect",
  client: "text-status-client",
  server: "text-status-server",
  pending: "text-muted-foreground",
}

export function deviceName(d: Pick<Device, "label" | "id"> | undefined) {
  return d?.label || "未命名设备"
}

// "a164 c75b 185c …": fingerprints are compared by eye against a phone screen.
export function groupHex(hex: string, size = 4) {
  return hex.match(new RegExp(`.{1,${size}}`, "g"))?.join(" ") ?? hex
}

// Short labels for the kind of content a response carries.
export function contentKind(contentType?: string, status?: number) {
  if (status === 101) return "ws"
  if (!contentType) return ""
  if (contentType.includes("json")) return "json"
  if (contentType.includes("html")) return "html"
  if (contentType.includes("javascript") || contentType.includes("ecmascript")) return "js"
  if (contentType.includes("css")) return "css"
  if (contentType.startsWith("image/")) return "img"
  if (contentType.includes("xml")) return "xml"
  if (contentType.startsWith("text/")) return "text"
  if (contentType.startsWith("font/")) return "font"
  if (contentType.startsWith("video/") || contentType.startsWith("audio/")) return "media"
  if (contentType.includes("protobuf") || contentType.includes("grpc")) return "proto"
  return "bin"
}
