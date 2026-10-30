import { useState } from "react"
import { Link } from "react-router"
import { PencilIcon, PlusIcon, ScrollTextIcon, Trash2Icon, TriangleAlertIcon } from "lucide-react"

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
import { Switch } from "@/components/ui/switch"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { Device, ModuleSummary, RuleList } from "@/lib/api"
import { formatRelative } from "@/lib/format"
import { errorMessage, useActivateModule, useDevices, useModules, useRemoveModule } from "@/lib/queries"
import { cn } from "@/lib/utils"
import { DevicePicker } from "./devices"
import { LogsDialog } from "./logs-dialog"

export const ruleLabels: Record<RuleList, string> = {
  header_rewrites: "Header 改写",
  url_rewrites: "URL 改写",
  map_locals: "本地映射",
  body_rewrites: "Body 改写",
  scripts: "脚本",
}

export function ModulesPage() {
  const modules = useModules()
  const devices = useDevices()
  const [removing, setRemoving] = useState<ModuleSummary | null>(null)
  const [logs, setLogs] = useState<ModuleSummary | null>(null)
  const items = modules.data ?? []
  return (
    <div className="mx-auto max-w-5xl space-y-4 px-4 py-6">
      <div className="flex flex-wrap items-end gap-x-6 gap-y-3">
        <div className="mr-auto max-w-prose">
          <h1 className="text-xl font-semibold tracking-tight">模块</h1>
          <p className="text-[13px] leading-relaxed text-muted-foreground">
            模块列出要解密的主机名，以及对这些主机的请求做什么：改写 Header、URL 和
            Body，返回本地数据，或者运行脚本。启用后只作用于选中的受信设备，其他设备访问这些主机不经过服务。
          </p>
        </div>
        <Button asChild>
          <Link to="/modules/new">
            <PlusIcon />
            新建模块
          </Link>
        </Button>
      </div>
      {modules.error && <p className="text-[13px] text-destructive">读取模块失败：{errorMessage(modules.error)}</p>}
      <ol className="space-y-3">
        {items.map((m) => (
          <ModuleCard
            key={m.id}
            module={m}
            devices={devices.data ?? []}
            onLogs={() => setLogs(m)}
            onRemove={() => setRemoving(m)}
          />
        ))}
      </ol>
      {modules.data && items.length === 0 && (
        <p className="rounded-lg border border-dashed px-4 py-6 text-center text-[13px] text-muted-foreground">
          还没有模块。没有启用的模块时，抓包之外的流量都不经过服务。
        </p>
      )}
      <p className="text-xs leading-relaxed text-muted-foreground">
        同一设备上的多个模块按列表顺序生效。正在抓包的设备照样执行它的模块，记录里是设备一侧看到的请求和响应。停用、删除和改动规则对下一个请求生效；改动设备范围只影响之后新建的连接。证书固定的
        App 访问模块里的主机会失败。
      </p>
      <LogsDialog module={logs} onOpenChange={(o) => !o && setLogs(null)} />
      <RemoveDialog module={removing} onOpenChange={(o) => !o && setRemoving(null)} />
    </div>
  )
}

function ModuleCard({
  module: m,
  devices,
  onLogs,
  onRemove,
}: {
  module: ModuleSummary
  devices: Device[]
  onLogs: () => void
  onRemove: () => void
}) {
  const activate = useActivateModule()
  const effective = devices.filter((d) => d.trusted && m.devices.includes(d.id))
  const rules = (Object.keys(ruleLabels) as RuleList[]).filter((k) => m.rules[k] > 0)
  return (
    <li className={cn("rounded-xl border bg-card p-4 lift", !m.enabled && "bg-subtle/40")}>
      <div className="flex flex-wrap items-start gap-3">
        <Switch
          checked={m.enabled}
          disabled={activate.isPending}
          onCheckedChange={(enabled) => activate.mutate([m.id, enabled])}
          aria-label={m.enabled ? `停用 ${m.name}` : `启用 ${m.name}`}
          className="mt-1"
        />
        <div className="min-w-0 flex-1 space-y-1">
          <h3 className="font-semibold">
            <Link to={`/modules/${m.id}`} className="hover:text-signal hover:underline">
              {m.name}
            </Link>
            <span className="ml-2 text-xs font-normal text-muted-foreground">{m.enabled ? "已启用" : "已停用"}</span>
          </h3>
          <p className="text-xs text-muted-foreground">
            {rules.length > 0 ? rules.map((k) => `${ruleLabels[k]} ${m.rules[k]}`).join(" · ") : "没有规则"}
            <span className="ml-3">{formatRelative(m.updated_at)}修改</span>
          </p>
        </div>
        <div className="flex items-center gap-0.5">
          <IconAction label="日志" onClick={onLogs}>
            <ScrollTextIcon />
          </IconAction>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button variant="ghost" size="icon-sm" asChild>
                <Link to={`/modules/${m.id}`} aria-label={`编辑 ${m.name}`}>
                  <PencilIcon />
                </Link>
              </Button>
            </TooltipTrigger>
            <TooltipContent>编辑</TooltipContent>
          </Tooltip>
          <IconAction label="删除" onClick={onRemove}>
            <Trash2Icon />
          </IconAction>
        </div>
      </div>
      <div className="mt-3 flex flex-wrap items-center gap-2 pl-11 text-[13px]">
        <span className="text-muted-foreground">作用于</span>
        <DevicePicker
          selected={m.devices}
          devices={devices}
          disabled={activate.isPending}
          onChange={(next) => activate.mutate([m.id, m.enabled, next])}
        />
        {m.enabled && effective.length === 0 && (
          <span className="flex items-center gap-1 text-xs text-warn-ink">
            <TriangleAlertIcon className="size-3.5" />
            没有受信设备，暂不生效
          </span>
        )}
        {m.enabled && m.hostnames.length === 0 && (
          <span className="flex items-center gap-1 text-xs text-warn-ink">
            <TriangleAlertIcon className="size-3.5" />
            没有主机名，不会解密任何请求
          </span>
        )}
      </div>
      {m.hostnames.length > 0 && (
        <ul className="mt-3 flex flex-wrap gap-1.5 pl-11">
          {m.hostnames.map((h, i) => (
            <li
              key={`${h}-${i}`}
              className={cn(
                "rounded border bg-background px-1.5 py-0.5 font-mono text-xs",
                h.startsWith("-") && "text-status-server"
              )}
            >
              {h}
            </li>
          ))}
        </ul>
      )}
    </li>
  )
}

function IconAction({ label, onClick, children }: { label: string; onClick: () => void; children: React.ReactNode }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button variant="ghost" size="icon-sm" aria-label={label} onClick={onClick}>
          {children}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  )
}

function RemoveDialog({
  module,
  onOpenChange,
}: {
  module: ModuleSummary | null
  onOpenChange: (open: boolean) => void
}) {
  const remove = useRemoveModule()
  return (
    <AlertDialog open={!!module} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>删除 {module?.name}？</AlertDialogTitle>
          <AlertDialogDescription>
            它的规则、脚本和日志一起删除，下一个请求起不再生效。脚本用 $persistentStore 保存的无键数据也会删除。
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>取消</AlertDialogCancel>
          <AlertDialogAction variant="danger" onClick={() => module && remove.mutate([module.id])}>
            删除模块
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
