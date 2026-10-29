import { useState } from "react"
import { toast } from "sonner"

import { CertificateDownload, Fingerprint, InstallSteps } from "@/components/ca-guide"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import type { Device } from "@/lib/api"
import { deviceName } from "@/lib/format"
import { useSetTrust, useStatus } from "@/lib/queries"

export function TrustDialog({
  device,
  onOpenChange,
}: {
  device: Device | null
  onOpenChange: (open: boolean) => void
}) {
  return (
    <Dialog open={!!device} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[92svh] overflow-y-auto sm:max-w-2xl">
        {device && <Form key={device.id} device={device} onDone={() => onOpenChange(false)} />}
      </DialogContent>
    </Dialog>
  )
}

function Step({ n, title, children }: { n: number; title: string; children: React.ReactNode }) {
  return (
    <li className="grid grid-cols-[1.75rem_minmax(0,1fr)] gap-x-2">
      <span className="flex size-6 items-center justify-center rounded-full border font-mono text-xs text-muted-foreground">
        {n}
      </span>
      <div className="space-y-2.5 pb-1">
        <h3 className="pt-0.5 text-[13px] font-semibold">{title}</h3>
        {children}
      </div>
    </li>
  )
}

function Form({ device, onDone }: { device: Device; onDone: () => void }) {
  const ca = useStatus().data?.ca
  const [evidence, setEvidence] = useState("")
  const trust = useSetTrust()
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault()
        trust.mutate([device.id, true, evidence.trim()], {
          onSuccess: () => {
            toast.success(`${deviceName(device)}已标记为受信`)
            onDone()
          },
        })
      }}
      className="contents"
    >
      <DialogHeader>
        <DialogTitle>确认{deviceName(device)}信任根证书</DialogTitle>
        <DialogDescription>
          服务读不到设备的证书信任设置，这里记下的是你的确认。确认错了，这台设备抓包时所有 HTTPS 都会报证书错误。
        </DialogDescription>
      </DialogHeader>
      {ca && (
        <ol className="space-y-5">
          <Step n={1} title="在设备上下载证书">
            <CertificateDownload ca={ca} size={128} />
          </Step>
          <Step n={2} title="安装并设为完全信任">
            <InstallSteps />
          </Step>
          <Step n={3} title="核对指纹">
            <p className="text-[13px] text-muted-foreground">
              设备上证书详情里的 SHA-256 指纹应该和这里一致，一致才说明装的是当前这张根证书。
            </p>
            <Fingerprint value={ca.fingerprint} />
          </Step>
          <Step n={4} title="写下谁、怎样确认的">
            <Label htmlFor="evidence" className="sr-only">
              确认记录
            </Label>
            <Textarea
              id="evidence"
              required
              value={evidence}
              onChange={(e) => setEvidence(e.target.value)}
              placeholder="例：张三本人在 iPhone 的证书信任设置里打开了完全信任，指纹已核对"
              className="min-h-20 text-[13px]"
            />
          </Step>
        </ol>
      )}
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onDone}>
          取消
        </Button>
        <Button type="submit" disabled={!evidence.trim() || trust.isPending}>
          标记为受信
        </Button>
      </DialogFooter>
    </form>
  )
}
