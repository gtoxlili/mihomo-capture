import { useState } from "react"
import { TriangleAlertIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import type { Device } from "@/lib/api"
import { deviceName } from "@/lib/format"
import { useStartCapture } from "@/lib/queries"

type Props = {
  device: Device | null
  current?: Device
  onOpenChange: (open: boolean) => void
}

export function CaptureDialog({ device, current, onOpenChange }: Props) {
  return (
    <Dialog open={!!device} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        {device && <Form key={device.id} device={device} current={current} onDone={() => onOpenChange(false)} />}
      </DialogContent>
    </Dialog>
  )
}

function parsePorts(text: string): number[] | null {
  const parts = text.split(/[\s,，]+/).filter(Boolean)
  const ports = parts.map(Number)
  return ports.every((p) => Number.isInteger(p) && p >= 1 && p <= 65535) ? ports : null
}

function Form({ device, current, onDone }: { device: Device; current?: Device; onDone: () => void }) {
  const [quic, setQuic] = useState(true)
  const [extra, setExtra] = useState("")
  const start = useStartCapture()
  const extraPorts = parsePorts(extra)
  const ports = [...new Set([...(quic ? [443] : []), ...(extraPorts ?? [])])].sort((a, b) => a - b)
  const replacing = !!current && current.id !== device.id

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault()
        start.mutate([device.id, ports, replacing], { onSuccess: onDone })
      }}
      className="contents"
    >
      <DialogHeader>
        <DialogTitle>抓包：{deviceName(device)}</DialogTitle>
        <DialogDescription className="font-mono text-xs">
          {device.ips.join(", ")}
          <span className="ml-3">{device.mac}</span>
        </DialogDescription>
      </DialogHeader>
      <div className="space-y-4 text-[13px]">
        <div className="flex items-start gap-3">
          <Checkbox id="quic" checked={quic} onCheckedChange={(v) => setQuic(v === true)} className="mt-0.5" />
          <div className="space-y-1">
            <Label htmlFor="quic">阻断 UDP 443，让走 HTTP/3 的 App 回落到 TCP</Label>
            <p className="text-muted-foreground">
              HTTP/3 走 UDP，解不了。阻断后这些 App 改用 TCP，请求才能被抓到；抓包期间这台设备发往 443 端口的其他 UDP
              也会断。
            </p>
          </div>
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="ports" className="font-normal text-muted-foreground">
            另外阻断的 UDP 端口（可选，逗号分隔）
          </Label>
          <Input
            id="ports"
            value={extra}
            onChange={(e) => setExtra(e.target.value)}
            placeholder="例如 8443"
            aria-invalid={extraPorts === null}
            className="h-8 font-mono text-xs"
          />
          {extraPorts === null && <p className="text-destructive">端口要是 1 到 65535 之间的数字。</p>}
        </div>
        <ul className="list-disc space-y-1 pl-5 text-muted-foreground">
          <li>只对开始之后新建立的连接生效。开始后在设备上重新打开 App 或刷新网页。</li>
          <li>证书固定的 App 在抓包期间连不上。设备的出网路径和分流不变。</li>
          <li>记录里是这台设备主人的登录态和个人数据。</li>
        </ul>
        {replacing && (
          <p className="flex items-start gap-2 rounded-md border border-warn-line bg-warn-wash px-3 py-2 text-warn-ink">
            <TriangleAlertIcon className="mt-0.5 size-4 shrink-0" />
            同一时间只能抓一台。开始后对{deviceName(current)}的抓包会先停止。
          </p>
        )}
      </div>
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onDone}>
          取消
        </Button>
        <Button type="submit" disabled={start.isPending || extraPorts === null}>
          {replacing ? "切换并开始抓包" : "开始抓包"}
        </Button>
      </DialogFooter>
    </form>
  )
}
