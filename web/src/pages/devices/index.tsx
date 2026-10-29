import { useState } from "react"
import {
  CheckIcon,
  CopyIcon,
  EllipsisIcon,
  PencilIcon,
  RefreshCwIcon,
  SearchIcon,
  ShieldOffIcon,
  SquareIcon,
  TriangleAlertIcon,
} from "lucide-react"
import { toast } from "sonner"

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { Device } from "@/lib/api"
import { deviceName } from "@/lib/format"
import { copyText } from "@/lib/hooks"
import {
  errorMessage,
  useDevices,
  useRefreshDevices,
  useRenameDevice,
  useSetTrust,
  useStatus,
  useStopCapture,
} from "@/lib/queries"
import { cn } from "@/lib/utils"
import { CaptureDialog } from "./capture-dialog"
import { TrustDialog } from "./trust-dialog"

type Show = "all" | "online" | "trusted"

// Capturing first, then what can be captured now, then the rest.
function rank(d: Device) {
  if (d.capturing) return 0
  if (d.trusted && d.online) return 1
  if (d.online) return 2
  if (d.trusted) return 3
  return 4
}

const collator = new Intl.Collator("zh-CN")

export function DevicesPage() {
  const devices = useDevices()
  const status = useStatus()
  const refresh = useRefreshDevices()
  const [search, setSearch] = useState("")
  const [show, setShow] = useState<Show>("all")
  const [capturing, setCapturing] = useState<Device | null>(null)
  const [trusting, setTrusting] = useState<Device | null>(null)
  const [revoking, setRevoking] = useState<Device | null>(null)

  const all = devices.data ?? []
  const current = all.find((d) => d.capturing)
  const needle = search.trim().toLowerCase()
  const shown = all
    .filter((d) => (show === "online" ? d.online : show === "trusted" ? d.trusted : true))
    .filter((d) => !needle || [d.label, d.mac, ...d.ips].some((s) => s.toLowerCase().includes(needle)))
    .sort((a, b) => rank(a) - rank(b) || collator.compare(a.label || "￿", b.label || "￿") || a.id.localeCompare(b.id))
  const fingerprint = status.data?.ca.fingerprint

  return (
    <div className="mx-auto max-w-6xl px-4 py-6">
      <div className="mb-4 flex flex-wrap items-end gap-x-6 gap-y-3">
        <div className="mr-auto">
          <h1 className="text-xl font-semibold tracking-tight">设备</h1>
          <p className="text-[13px] text-muted-foreground">
            {all.length} 台见过的设备，{all.filter((d) => d.online).length} 台在线，
            {all.filter((d) => d.trusted).length} 台已确认信任根证书。只有受信且在线的设备能抓包。
          </p>
        </div>
        <div className="relative w-56">
          <SearchIcon className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            aria-label="搜索设备"
            placeholder="名称、MAC 或 IP"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="h-8 pl-8"
          />
        </div>
        <ToggleGroup
          type="single"
          variant="segmented"
          size="sm"
          value={show}
          onValueChange={(v) => v && setShow(v as Show)}
        >
          <ToggleGroupItem value="all" className="h-7 px-3">
            全部
          </ToggleGroupItem>
          <ToggleGroupItem value="online" className="h-7 px-3">
            在线
          </ToggleGroupItem>
          <ToggleGroupItem value="trusted" className="h-7 px-3">
            受信
          </ToggleGroupItem>
        </ToggleGroup>
        <Tooltip>
          <TooltipTrigger asChild>
            <Button variant="outline" size="sm" disabled={refresh.isPending} onClick={() => refresh.mutate([])}>
              <RefreshCwIcon className={cn(refresh.isPending && "animate-spin")} />
              刷新
            </Button>
          </TooltipTrigger>
          <TooltipContent>立即刷新设备来源地址；平时每 5 秒自动刷新</TooltipContent>
        </Tooltip>
      </div>

      <div className="overflow-x-auto rounded-xl border bg-card lift">
        <table className="w-full min-w-[46rem] text-left text-[13px]">
          <thead className="border-b bg-subtle/60 text-[11.5px] text-muted-foreground">
            <tr className="[&>th]:h-9 [&>th]:px-4 [&>th]:font-normal">
              <th className="w-8" aria-label="在线" />
              <th>名称</th>
              <th>MAC</th>
              <th>IP</th>
              <th>根证书</th>
              <th className="w-44" aria-label="操作" />
            </tr>
          </thead>
          <tbody>
            {devices.isPending && (
              <tr>
                <td colSpan={6} className="px-3 py-8 text-center text-muted-foreground">
                  正在读取设备…
                </td>
              </tr>
            )}
            {devices.error && (
              <tr>
                <td colSpan={6} className="px-3 py-8 text-center text-destructive">
                  读取设备失败：{errorMessage(devices.error)}
                </td>
              </tr>
            )}
            {devices.data && shown.length === 0 && (
              <tr>
                <td colSpan={6} className="px-3 py-8 text-center text-muted-foreground">
                  没有符合条件的设备。
                </td>
              </tr>
            )}
            {shown.map((d) => (
              <DeviceRow
                key={d.id}
                device={d}
                fingerprint={fingerprint}
                onCapture={() => setCapturing(d)}
                onTrust={() => setTrusting(d)}
                onRevoke={() => setRevoking(d)}
              />
            ))}
          </tbody>
        </table>
      </div>
      <p className="mt-3 text-xs leading-relaxed text-muted-foreground">
        本机模式自动使用当前电脑的地址。网关模式按 MAC 识别设备，IP 来自邻居表；在线表示有 IP 绑定。手机的私有 Wi-Fi
        地址轮换后会以新设备出现，信任不会跟过去。
      </p>

      <CaptureDialog device={capturing} current={current} onOpenChange={(o) => !o && setCapturing(null)} />
      <TrustDialog device={trusting} onOpenChange={(o) => !o && setTrusting(null)} />
      <RevokeDialog device={revoking} onOpenChange={(o) => !o && setRevoking(null)} />
    </div>
  )
}

type RowProps = {
  device: Device
  fingerprint?: string
  onCapture: () => void
  onTrust: () => void
  onRevoke: () => void
}

function DeviceRow({ device: d, fingerprint, onCapture, onTrust, onRevoke }: RowProps) {
  const [editing, setEditing] = useState(false)
  const stop = useStopCapture()
  const rotated = !d.trusted && !!d.trust_evidence && !!d.ca_fingerprint && d.ca_fingerprint !== fingerprint
  return (
    <tr
      className={cn(
        "border-b border-line/70 transition-colors last:border-b-0 hover:bg-subtle/50 [&>td]:h-[3.25rem] [&>td]:px-4",
        d.capturing && "bg-tally-wash hover:bg-tally-wash",
        !d.online && "text-muted-foreground"
      )}
    >
      <td>
        <span
          aria-label={d.online ? "在线" : "离线"}
          className={cn(
            "block size-2 rounded-full",
            d.capturing ? "animate-tally bg-tally" : d.online ? "bg-presence" : "ring-1 ring-faint ring-inset"
          )}
        />
      </td>
      <td className="max-w-64">
        {editing ? (
          <LabelEditor device={d} onDone={() => setEditing(false)} />
        ) : (
          <button
            type="button"
            onClick={() => setEditing(true)}
            className="group flex max-w-full items-center gap-1.5 text-left"
            aria-label={`修改${deviceName(d)}的名称`}
          >
            <span className={cn("truncate font-medium", !d.label && "font-normal text-muted-foreground italic")}>
              {deviceName(d)}
            </span>
            <PencilIcon className="size-3 shrink-0 text-muted-foreground opacity-0 group-hover:opacity-100 group-focus-visible:opacity-100" />
          </button>
        )}
      </td>
      <td className="font-mono text-xs text-ink-2">{d.id === "local" ? "本机" : d.mac}</td>
      <td className="font-mono text-xs text-ink-2">{d.ips.length ? d.ips.join(", ") : "—"}</td>
      <td>
        {d.trusted ? (
          <Tooltip>
            <TooltipTrigger className="inline-flex items-center gap-1 rounded-full bg-signal-wash px-2 py-0.5 text-xs font-medium text-signal">
              <CheckIcon className="size-3" />
              已确认
            </TooltipTrigger>
            <TooltipContent className="max-w-72">{d.trust_evidence}</TooltipContent>
          </Tooltip>
        ) : rotated ? (
          <span className="inline-flex items-center gap-1 rounded-full bg-warn-wash px-2 py-0.5 text-xs font-medium text-warn-ink">
            <TriangleAlertIcon className="size-3" />
            根证书已更换
          </span>
        ) : (
          <span className="text-xs text-faint">未确认</span>
        )}
      </td>
      <td>
        <div className="flex items-center justify-end gap-1">
          {d.capturing ? (
            <Button variant="danger" size="sm" disabled={stop.isPending} onClick={() => stop.mutate([])}>
              <SquareIcon className="size-3 fill-current" />
              停止抓包
            </Button>
          ) : d.trusted ? (
            <Tooltip>
              <TooltipTrigger asChild>
                <span tabIndex={d.online ? -1 : 0}>
                  <Button size="sm" disabled={!d.online} onClick={onCapture}>
                    开始抓包
                  </Button>
                </span>
              </TooltipTrigger>
              {!d.online && <TooltipContent>设备当前没有可用的来源地址</TooltipContent>}
            </Tooltip>
          ) : (
            <Button variant="outline" size="sm" onClick={onTrust}>
              确认信任…
            </Button>
          )}
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" size="icon-sm" aria-label={`${deviceName(d)}的更多操作`}>
                <EllipsisIcon />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem onSelect={() => setEditing(true)}>
                <PencilIcon />
                改名
              </DropdownMenuItem>
              <DropdownMenuItem disabled={d.id === "local"} onSelect={() => copyText(d.mac).then(() => toast.success("已复制 MAC"))}>
                <CopyIcon />
                复制 MAC
              </DropdownMenuItem>
              {d.trusted ? (
                <>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem variant="destructive" onSelect={onRevoke}>
                    <ShieldOffIcon />
                    撤销信任…
                  </DropdownMenuItem>
                </>
              ) : (
                <DropdownMenuItem onSelect={onTrust}>
                  <CheckIcon />
                  确认信任…
                </DropdownMenuItem>
              )}
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </td>
    </tr>
  )
}

function LabelEditor({ device, onDone }: { device: Device; onDone: () => void }) {
  const [value, setValue] = useState(device.label)
  const rename = useRenameDevice()
  function save() {
    const label = value.trim()
    if (label === device.label) return onDone()
    rename.mutate([device.id, label], { onSettled: onDone })
  }
  return (
    <Input
      autoFocus
      aria-label="设备名称"
      value={value}
      maxLength={200}
      placeholder="例如 张三 / iPhone"
      onChange={(e) => setValue(e.target.value)}
      onBlur={save}
      onKeyDown={(e) => {
        if (e.key === "Enter") save()
        if (e.key === "Escape") onDone()
      }}
      className="h-7 text-[13px]"
    />
  )
}

function RevokeDialog({ device, onOpenChange }: { device: Device | null; onOpenChange: (open: boolean) => void }) {
  const trust = useSetTrust()
  return (
    <AlertDialog open={!!device} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>撤销{deviceName(device ?? undefined)}的信任？</AlertDialogTitle>
          <AlertDialogDescription>
            撤销后这台设备不再被解密，也不能被选中抓包。
            {device?.capturing && "正在进行的抓包会随之停止。"}
            之后要再抓它，需要重新确认一次。
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>取消</AlertDialogCancel>
          <AlertDialogAction
            variant="danger"
            onClick={() =>
              device &&
              trust.mutate([device.id, false], {
                onSuccess: () => toast.success(`已撤销${deviceName(device)}的信任`),
              })
            }
          >
            撤销信任
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
