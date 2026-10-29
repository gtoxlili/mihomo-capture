import type { Body, Headers } from "./api"

export type Language = "json" | "html" | "xml" | "javascript" | "css" | "text"

export type BodyView =
  | { kind: "empty" }
  | { kind: "text"; text: string; language: Language }
  | { kind: "form"; text: string; fields: [string, string][] }
  | { kind: "image"; src: string }
  | { kind: "binary"; bytes: Uint8Array }

export function header(headers: Headers, name: string) {
  if (!headers) return ""
  const key = Object.keys(headers).find((k) => k.toLowerCase() === name.toLowerCase())
  return key ? (headers[key]?.[0] ?? "") : ""
}

export function headerEntries(headers: Headers): [string, string][] {
  if (!headers) return []
  return Object.entries(headers)
    .sort(([a], [b]) => a.localeCompare(b))
    .flatMap(([k, vs]) => vs.map((v) => [k, v] as [string, string]))
}

export function base64Bytes(data: string) {
  const raw = atob(data)
  const out = new Uint8Array(raw.length)
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i)
  return out
}

export function bodyBytes(body: Body) {
  return body.encoding === "base64" ? base64Bytes(body.data) : new TextEncoder().encode(body.data)
}

function languageOf(mediaType: string, text: string): Language {
  if (mediaType.includes("json")) return "json"
  if (mediaType.includes("html")) return "html"
  if (mediaType.includes("xml") || mediaType.includes("svg")) return "xml"
  if (mediaType.includes("javascript") || mediaType.includes("ecmascript")) return "javascript"
  if (mediaType.includes("css")) return "css"
  const t = text.trimStart()
  if ((t.startsWith("{") || t.startsWith("[")) && isJSON(text)) return "json"
  if (t.startsWith("<")) return "html"
  return "text"
}

function isJSON(text: string) {
  try {
    JSON.parse(text)
    return true
  } catch {
    return false
  }
}

// Pretty-printing re-indents valid JSON only; anything else is shown as sent.
export function prettyJSON(text: string) {
  try {
    return JSON.stringify(JSON.parse(text), null, 2)
  } catch {
    return text
  }
}

// viewBody decides how a captured body is shown. The decoded view of a
// compressed body is preferred, since its wire bytes are unreadable.
export function viewBody(body: Body | undefined, contentType: string, useDecoded = true): BodyView {
  if (!body || body.size === 0) return { kind: "empty" }
  const b = useDecoded && body.decoded ? body.decoded : body
  const mediaType = contentType.split(";")[0].trim().toLowerCase()
  if (b.encoding === "utf8") {
    if (mediaType === "application/x-www-form-urlencoded") {
      return { kind: "form", text: b.data, fields: [...new URLSearchParams(b.data)] }
    }
    return { kind: "text", text: b.data, language: languageOf(mediaType, b.data) }
  }
  if (mediaType.startsWith("image/") && b === body && !body.content_encoding) {
    return { kind: "image", src: `data:${mediaType};base64,${b.data}` }
  }
  return { kind: "binary", bytes: base64Bytes(b.data) }
}

export const opcodeName: Record<number, string> = {
  0: "续帧",
  1: "文本",
  2: "二进制",
  8: "关闭",
  9: "Ping",
  10: "Pong",
}
