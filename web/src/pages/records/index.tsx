import { useEffect, useRef, useState } from "react"
import { useSearchParams } from "react-router"
import { EllipsisIcon, SearchIcon, Trash2Icon, XIcon } from "lucide-react"

import { ClearRecordsDialog } from "@/components/clear-records"
import { Button } from "@/components/ui/button"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from "@/components/ui/resizable"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import type { RecordFilter } from "@/lib/api"
import { deviceName } from "@/lib/format"
import { useDebouncedCommit, useMediaQuery } from "@/lib/hooks"
import { useDevices, useRecords, useStatus } from "@/lib/queries"
import { cn } from "@/lib/utils"
import { RecordDetail } from "./detail"
import { RecordList } from "./list"

const filterKeys = ["host", "path", "q", "method", "status", "device"] as const
const methods = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"]

export function RecordsPage() {
  const [params, setParams] = useSearchParams()
  const [live, setLive] = useState(true)
  const [clearing, setClearing] = useState(false)
  const wide = useMediaQuery("(min-width: 1024px)")
  const hostInput = useRef<HTMLInputElement>(null)
  const status = useStatus()
  const devices = useDevices()

  const filter: RecordFilter = {}
  for (const k of filterKeys) {
    const v = params.get(k)
    if (v) filter[k] = v
  }
  const selected = params.get("r")

  function update(patch: Record<string, string | null>) {
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev)
        for (const [k, v] of Object.entries(patch)) {
          if (v) next.set(k, v)
          else next.delete(k)
        }
        return next
      },
      { replace: true }
    )
  }

  const records = useRecords(filter, live)
  const items = records.data?.pages.flatMap((p) => p.items) ?? []

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const typing = e.target instanceof HTMLElement && e.target.closest("input, textarea, [contenteditable]")
      if (e.key === "/" && !typing) {
        e.preventDefault()
        hostInput.current?.focus()
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [])

  const filtered = filterKeys.some((k) => params.get(k))
  const capture = status.data?.capture
  const captureDevice = devices.data?.find((d) => d.id === capture?.device_id)

  const list = (
    <RecordList
      items={items}
      devices={devices.data ?? []}
      selected={selected}
      onSelect={(id) => update({ r: id })}
      hasMore={!!records.hasNextPage}
      loadMore={() => !records.isFetchingNextPage && records.fetchNextPage()}
      loading={records.isPending}
      error={records.error}
      filtered={filtered}
      capture={capture ?? null}
      captureDevice={captureDevice}
      onClearFilters={() => update(Object.fromEntries(filterKeys.map((k) => [k, null])))}
    />
  )
  const detail = selected ? (
    <RecordDetail
      id={selected}
      devices={devices.data ?? []}
      onClose={() => update({ r: null })}
      onFilter={(patch) => update(patch)}
    />
  ) : null

  return (
    <div className="flex h-full flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b bg-card px-3 py-2">
        <FilterInput
          ref={hostInput}
          label="Host"
          placeholder="host 包含"
          value={params.get("host") ?? ""}
          onCommit={(v) => update({ host: v })}
          icon
          hint="/"
        />
        <FilterInput
          label="路径"
          placeholder="路径包含"
          value={params.get("path") ?? ""}
          onCommit={(v) => update({ path: v })}
        />
        <FilterInput
          label="全文"
          placeholder="头、正文、帧里包含"
          value={params.get("q") ?? ""}
          onCommit={(v) => update({ q: v })}
          className="w-44"
        />
        <Select value={params.get("method") ?? "all"} onValueChange={(v) => update({ method: v === "all" ? null : v })}>
          <SelectTrigger size="sm" className="w-28 font-mono text-xs" aria-label="方法">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all" className="font-sans">
              全部方法
            </SelectItem>
            {methods.map((m) => (
              <SelectItem key={m} value={m} className="font-mono text-xs">
                {m}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <StatusInput value={params.get("status") ?? ""} onCommit={(v) => update({ status: v })} />
        <Select value={params.get("device") ?? "all"} onValueChange={(v) => update({ device: v === "all" ? null : v })}>
          <SelectTrigger size="sm" className="w-44" aria-label="设备">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">全部设备</SelectItem>
            {(devices.data ?? [])
              .filter((d) => d.trusted || d.id === params.get("device"))
              .map((d) => (
                <SelectItem key={d.id} value={d.id}>
                  {deviceName(d)}
                  {d.capturing ? "（抓包中）" : ""}
                </SelectItem>
              ))}
          </SelectContent>
        </Select>
        {filtered && (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => update(Object.fromEntries(filterKeys.map((k) => [k, null])))}
          >
            <XIcon />
            清除筛选
          </Button>
        )}
        <div className="ml-auto flex items-center gap-3">
          <span className="text-xs text-muted-foreground tabular">
            {records.isPending ? "" : `已载入 ${items.length} 条${records.hasNextPage ? "，滚动加载更多" : ""}`}
          </span>
          <div className="flex items-center gap-2">
            <Switch id="live" checked={live} onCheckedChange={setLive} size="sm" />
            <Label htmlFor="live" className="text-[13px] font-normal">
              实时
            </Label>
          </div>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" size="icon-sm" aria-label="更多操作">
                <EllipsisIcon />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem variant="destructive" onSelect={() => setClearing(true)}>
                <Trash2Icon />
                清空全部记录…
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>
      <div className="min-h-0 flex-1">
        {wide && detail ? (
          <ResizablePanelGroup orientation="horizontal">
            <ResizablePanel id="list" minSize={360} defaultSize="44%">
              {list}
            </ResizablePanel>
            <ResizableHandle />
            <ResizablePanel id="detail" minSize={420}>
              {detail}
            </ResizablePanel>
          </ResizablePanelGroup>
        ) : (
          <>
            {list}
            {detail && <div className="fixed inset-0 z-40 bg-card">{detail}</div>}
          </>
        )}
      </div>
      <ClearRecordsDialog open={clearing} onOpenChange={setClearing} onCleared={() => update({ r: null })} />
    </div>
  )
}

type FilterInputProps = {
  label: string
  placeholder: string
  value: string
  onCommit: (v: string) => void
  className?: string
  icon?: boolean
  hint?: string
  ref?: React.Ref<HTMLInputElement>
}

// Typing is debounced into the URL; a value set elsewhere (a "filter by this
// host" click) replaces what is in the box.
function FilterInput({ label, placeholder, value, onCommit, className, icon, hint, ref }: FilterInputProps) {
  const [text, setText] = useSyncedState(value)
  const commit = useDebouncedCommit(onCommit)
  return (
    <div className={cn("relative w-36", className)}>
      {icon && (
        <SearchIcon className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground" />
      )}
      <Input
        ref={ref}
        aria-label={label}
        placeholder={placeholder}
        value={text}
        onChange={(e) => {
          setText(e.target.value)
          commit(e.target.value.trim())
        }}
        onKeyDown={(e) => {
          if (e.key === "Escape") {
            setText("")
            commit("", 0)
          }
        }}
        className={cn("h-7 font-mono text-xs placeholder:font-sans", icon && "pl-7", hint && "pr-6")}
      />
      {hint && !text && (
        <kbd className="pointer-events-none absolute top-1/2 right-1.5 -translate-y-1/2 rounded border bg-muted px-1 font-mono text-[10px] text-muted-foreground">
          {hint}
        </kbd>
      )}
    </div>
  )
}

function StatusInput({ value, onCommit }: { value: string; onCommit: (v: string) => void }) {
  const [text, setText] = useSyncedState(value)
  const commit = useDebouncedCommit(onCommit)
  const valid = (v: string) => v === "" || (Number(v) >= 100 && Number(v) <= 599)
  return (
    <Input
      aria-label="状态码"
      placeholder="状态码"
      inputMode="numeric"
      maxLength={3}
      value={text}
      onChange={(e) => {
        const v = e.target.value.replace(/\D/g, "")
        setText(v)
        if (valid(v)) commit(v)
      }}
      aria-invalid={!valid(text)}
      className="h-7 w-20 font-mono text-xs placeholder:font-sans"
    />
  )
}

// Local text that follows the URL whenever the URL changes underneath it.
function useSyncedState(value: string) {
  const [text, setText] = useState(value)
  const [synced, setSynced] = useState(value)
  if (value !== synced) {
    setSynced(value)
    setText(value)
  }
  return [text, setText] as const
}
