// Wire types of the service's /v1 API; the Go types in internal/app are the
// source of truth.

export type Device = {
  id: string
  mac: string
  label: string
  ips: string[]
  trusted: boolean
  trust_evidence?: string
  ca_fingerprint?: string
  online: boolean
  capturing: boolean
}

export type Capture = {
  device_id: string
  started_at: string
  block_udp_ports?: number[]
  generation: number
}

export type Status = {
  api_version: number
  capture: Capture | null
  hostnames: string[]
  ca: { name: string; fingerprint: string; not_after: string; lan_url: string }
  gateway: {
    version: string
    catalog: boolean
    rules: Record<string, number> | null
  } | null
  gateway_error: string
  storage: {
    records: number
    bytes: number
    max_records: number
    max_bytes: number
    retention_hours: number
    last_error: string
  }
}

export type Module = {
  id: string
  name: string
  source: string
  hostnames: string[]
  sha256: string
  loaded_at: string
  processing: string
}

export type Body = {
  encoding: "utf8" | "base64"
  data: string
  size: number
  truncated: boolean
  content_encoding?: string
  decoded?: Body
}

export type Headers = Record<string, string[]> | null

export type Message = { headers: Headers; trailers?: Headers; body: Body }

export type Frame = {
  at: string
  direction: "client_to_server" | "server_to_client"
  opcode: number
  final: boolean
  compressed: boolean
  body: Body
}

type RecordBase = {
  id: string
  device_id: string
  session: number
  at: string
  method: string
  url: string
  host: string
  path: string
  status: number
  duration_ms: number
  complete: boolean
  replayable: boolean
  error?: string
}

export type RecordSummary = RecordBase & {
  content_type?: string
  response_size: number
  frames?: number
}

export type CaptureRecord = RecordBase & {
  request: Message
  response: Message
  frames?: Frame[]
  frames_truncated?: boolean
  curl?: string
}

export type RecordFilter = {
  host?: string
  path?: string
  method?: string
  status?: string
  q?: string
  device?: string
}

export class ApiError extends Error {
  status: number
  code: string
  constructor(status: number, code: string, message: string) {
    super(message)
    this.status = status
    this.code = code
  }
}

// The token lives in this browser only: localStorage when remembered,
// sessionStorage otherwise.
const tokenKey = "mihomo-capture.token"
const listeners = new Set<() => void>()

export const token = {
  get: () => sessionStorage.getItem(tokenKey) ?? localStorage.getItem(tokenKey),
  set(value: string, remember: boolean) {
    token.clear()
    ;(remember ? localStorage : sessionStorage).setItem(tokenKey, value)
    listeners.forEach((l) => l())
  },
  clear() {
    sessionStorage.removeItem(tokenKey)
    localStorage.removeItem(tokenKey)
    listeners.forEach((l) => l())
  },
  subscribe(listener: () => void) {
    listeners.add(listener)
    return () => listeners.delete(listener)
  },
}

async function call<T>(method: string, path: string, body?: unknown, bearer = token.get()): Promise<T> {
  const headers: Record<string, string> = { Authorization: `Bearer ${bearer}` }
  if (body !== undefined) headers["Content-Type"] = "application/json"
  const res = await fetch(path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const payload = await res.json().catch(() => null)
  if (!res.ok) {
    const error = payload?.error ?? {}
    if (res.status === 401 && bearer === token.get()) token.clear()
    throw new ApiError(res.status, error.code ?? "unknown", error.message ?? `${res.status} ${res.statusText}`)
  }
  return payload as T
}

function query(params: Record<string, string | number | undefined>) {
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== "") q.set(k, String(v))
  }
  const s = q.toString()
  return s ? `?${s}` : ""
}

export const api = {
  verify: (candidate: string) => call<Status>("GET", "/v1/status", undefined, candidate),
  status: () => call<Status>("GET", "/v1/status"),
  devices: () => call<{ items: Device[] }>("GET", "/v1/devices"),
  refreshDevices: () => call("POST", "/v1/devices/refresh"),
  renameDevice: (id: string, label: string) => call("PUT", `/v1/devices/${encodeURIComponent(id)}`, { label }),
  setTrust: (id: string, trusted: boolean, evidence = "") =>
    call("PUT", `/v1/devices/${encodeURIComponent(id)}/trust`, {
      trusted,
      trust_evidence: evidence,
    }),
  startCapture: (deviceId: string, blockUDPPorts: number[]) =>
    call<Capture>("PUT", "/v1/capture", {
      device_id: deviceId,
      block_udp_ports: blockUDPPorts,
    }),
  stopCapture: () => call("DELETE", "/v1/capture"),
  modules: () => call<{ items: Module[]; hostnames: string[] }>("GET", "/v1/modules"),
  registerModule: (source: string, args: Record<string, string>) =>
    call<Module>("POST", "/v1/modules", { source, arguments: args }),
  removeModule: (id: string) => call("DELETE", `/v1/modules/${encodeURIComponent(id)}`),
  records: (filter: RecordFilter, before?: string, limit = 100) =>
    call<{ items: RecordSummary[]; next: string }>("GET", `/v1/records${query({ ...filter, before, limit })}`),
  record: (id: string) => call<CaptureRecord>("GET", `/v1/records/${encodeURIComponent(id)}`),
  clearRecords: () => call("DELETE", "/v1/records"),
}
