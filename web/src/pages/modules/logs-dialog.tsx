import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import type { ModuleLog, ModuleSummary } from "@/lib/api"
import { formatClock } from "@/lib/format"
import { errorMessage, useModuleLogs } from "@/lib/queries"
import { cn } from "@/lib/utils"

const levels: Record<ModuleLog["level"], { label: string; className: string }> = {
  info: { label: "日志", className: "text-muted-foreground" },
  notify: { label: "通知", className: "text-signal" },
  warn: { label: "注意", className: "text-status-client" },
  error: { label: "错误", className: "text-status-server" },
}

export function LogsDialog({
  module,
  onOpenChange,
}: {
  module: ModuleSummary | null
  onOpenChange: (o: boolean) => void
}) {
  const logs = useModuleLogs(module?.id ?? null)
  const items = logs.data ?? []
  return (
    <Dialog open={!!module} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>{module?.name} 的日志</DialogTitle>
          <DialogDescription>
            最近 200 条，最新的在前，只在内存里，服务重启后清空。脚本的 console 输出、$notification
            通知，以及被跳过的规则和失败的脚本都在这里；失败时请求和响应按原样放行。
          </DialogDescription>
        </DialogHeader>
        {logs.error && <p className="text-[13px] text-destructive">读取日志失败：{errorMessage(logs.error)}</p>}
        {logs.data && items.length === 0 && (
          <p className="py-6 text-center text-[13px] text-muted-foreground">还没有日志。</p>
        )}
        {items.length > 0 && (
          <ol className="max-h-[60vh] divide-y divide-line overflow-auto rounded-lg border text-[13px]">
            {items.map((l, i) => (
              <li key={i} className="space-y-0.5 px-3 py-2">
                <div className="flex items-baseline gap-2 text-xs">
                  <span className={cn("font-medium", levels[l.level].className)}>{levels[l.level].label}</span>
                  <span className="font-mono text-muted-foreground">{formatClock(l.at)}</span>
                  <span className="font-mono text-ink-2">{l.source}</span>
                  {l.url && <span className="min-w-0 truncate font-mono text-faint">{l.url}</span>}
                </div>
                <p className="font-mono text-xs break-all whitespace-pre-wrap">{l.message}</p>
              </li>
            ))}
          </ol>
        )}
      </DialogContent>
    </Dialog>
  )
}
