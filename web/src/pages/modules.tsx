import { useState } from "react"
import { PlusIcon, Trash2Icon, XIcon } from "lucide-react"

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
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { Module } from "@/lib/api"
import { formatRelative } from "@/lib/format"
import { errorMessage, useModules, useRegisterModule, useRemoveModule } from "@/lib/queries"
import { cn } from "@/lib/utils"

export function ModulesPage() {
  const modules = useModules()
  const [removing, setRemoving] = useState<Module | null>(null)
  const items = modules.data?.items ?? []
  const hostnames = modules.data?.hostnames ?? []
  return (
    <div className="mx-auto grid max-w-6xl gap-8 px-4 py-6 lg:grid-cols-[minmax(0,1fr)_20rem]">
      <div className="min-w-0 space-y-4">
        <div>
          <h1 className="text-xl font-semibold tracking-tight">模块</h1>
          <p className="max-w-prose text-[13px] leading-relaxed text-muted-foreground">
            Surge 模块（.sgmodule）决定日常解密哪些 hostname：受信设备访问这些 host 时一直被解密，但不记录。现在只登记
            hostname，模块里的脚本、改写和规则都不执行，所以不改动任何请求。证书固定的 App 访问这些 host
            会失败，波及所有受信设备。
          </p>
        </div>
        <Register count={items.length} />
        {modules.error && <p className="text-[13px] text-destructive">读取模块失败：{errorMessage(modules.error)}</p>}
        <ol className="space-y-3">
          {items.map((m, i) => (
            <ModuleItem key={m.id} module={m} order={i + 1} onRemove={() => setRemoving(m)} />
          ))}
        </ol>
        {modules.data && items.length === 0 && (
          <p className="rounded-lg border border-dashed px-4 py-6 text-center text-[13px] text-muted-foreground">
            还没有模块。没有模块时，日常不解密任何流量。
          </p>
        )}
      </div>
      <aside className="space-y-2 lg:sticky lg:top-6 lg:self-start">
        <h2 className="text-[13px] font-semibold">
          合成后的 hostname <span className="font-normal text-muted-foreground tabular">{hostnames.length}</span>
        </h2>
        <p className="text-xs leading-relaxed text-muted-foreground">
          按注册顺序合成：后注册的模块覆盖、追加（%APPEND%）或插到前面（%INSERT%），前缀减号表示排除。被抓包的设备解密全部
          host，这份清单对它没有额外影响。
        </p>
        {hostnames.length > 0 ? (
          <ul className="max-h-[60vh] overflow-auto rounded-xl border bg-card py-1.5 font-mono text-xs lift">
            {hostnames.map((h, i) => (
              <li key={`${h}-${i}`} className={cn("px-3 py-1", h.startsWith("-") && "text-status-server")}>
                {h.startsWith("-") ? (
                  <>
                    <span className="font-sans">排除 </span>
                    {h.slice(1)}
                  </>
                ) : (
                  h
                )}
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-[13px] text-muted-foreground">空</p>
        )}
      </aside>
      <RemoveDialog module={removing} onOpenChange={(o) => !o && setRemoving(null)} />
    </div>
  )
}

function ModuleItem({ module: m, order, onRemove }: { module: Module; order: number; onRemove: () => void }) {
  const remote = /^https?:\/\//.test(m.source)
  return (
    <li className="rounded-xl border bg-card p-4 lift">
      <div className="flex items-start gap-3">
        <span className="pt-0.5 font-mono text-xs text-muted-foreground tabular" aria-label={`第 ${order} 个注册`}>
          {order}
        </span>
        <div className="min-w-0 flex-1 space-y-1">
          <h3 className="font-semibold">{m.name}</h3>
          <p className="truncate font-mono text-xs text-muted-foreground">
            {remote ? (
              <a
                href={m.source}
                target="_blank"
                rel="noreferrer noopener"
                className="hover:text-signal hover:underline"
              >
                {m.source}
              </a>
            ) : (
              m.source
            )}
          </p>
          <p className="text-xs text-muted-foreground">
            {formatRelative(m.loaded_at)}注册
            <Tooltip>
              <TooltipTrigger className="ml-3 font-mono">sha256 {m.sha256.slice(0, 12)}</TooltipTrigger>
              <TooltipContent className="font-mono text-xs">{m.sha256}</TooltipContent>
            </Tooltip>
          </p>
        </div>
        <Button variant="ghost" size="icon-sm" aria-label={`移除 ${m.name}`} onClick={onRemove}>
          <Trash2Icon />
        </Button>
      </div>
      <ul className="mt-3 flex flex-wrap gap-1.5 pl-6">
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
    </li>
  )
}

function Register({ count }: { count: number }) {
  const [source, setSource] = useState("")
  const [args, setArgs] = useState<[string, string][]>([])
  const register = useRegisterModule()
  const full = count >= 32
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault()
        const values = Object.fromEntries(args.filter(([k]) => k.trim()).map(([k, v]) => [k.trim(), v]))
        register.mutate([source.trim(), values], {
          onSuccess: () => {
            setSource("")
            setArgs([])
          },
        })
      }}
      className="space-y-3 rounded-xl border bg-card p-4 lift"
    >
      <div className="space-y-1.5">
        <Label htmlFor="source">注册模块</Label>
        <div className="flex gap-2">
          <Input
            id="source"
            value={source}
            onChange={(e) => setSource(e.target.value)}
            placeholder="https://…/x.sgmodule，或 运行服务的电脑的绝对路径"
            className="h-8 font-mono text-xs placeholder:font-sans"
          />
          <Button type="submit" disabled={!source.trim() || register.isPending || full}>
            注册
          </Button>
        </div>
        <p className="text-xs text-muted-foreground">
          {full
            ? "已经有 32 个模块，先移除一个才能注册。"
            : "注册后 hostname 立即对所有受信设备生效。同一个来源要先移除才能重新注册；每个模块最大 2 MiB，最多 32 个。"}
        </p>
      </div>
      <div className="space-y-1.5">
        {args.map(([k, v], i) => (
          <div key={i} className="flex items-center gap-2">
            <Input
              aria-label="参数名"
              placeholder="参数名"
              value={k}
              onChange={(e) => setArgs(args.map((a, j) => (j === i ? [e.target.value, a[1]] : a)))}
              className="h-7 w-40 font-mono text-xs placeholder:font-sans"
            />
            <Input
              aria-label="参数值"
              placeholder="值"
              value={v}
              onChange={(e) => setArgs(args.map((a, j) => (j === i ? [a[0], e.target.value] : a)))}
              className="h-7 flex-1 font-mono text-xs placeholder:font-sans"
            />
            <Button
              type="button"
              variant="ghost"
              size="icon-xs"
              aria-label="删掉这个参数"
              onClick={() => setArgs(args.filter((_, j) => j !== i))}
            >
              <XIcon />
            </Button>
          </div>
        ))}
        <Button
          type="button"
          variant="ghost"
          size="xs"
          className="text-muted-foreground"
          onClick={() => setArgs([...args, ["", ""]])}
        >
          <PlusIcon />
          覆盖模块参数
        </Button>
        {args.length > 0 && (
          <p className="text-xs text-muted-foreground">
            参数名要是模块 #!arguments 里声明过的，没填的用模块自己的默认值。
          </p>
        )}
      </div>
    </form>
  )
}

function RemoveDialog({ module, onOpenChange }: { module: Module | null; onOpenChange: (open: boolean) => void }) {
  const remove = useRemoveModule()
  return (
    <AlertDialog open={!!module} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>移除 {module?.name}？</AlertDialogTitle>
          <AlertDialogDescription>
            它的 {module?.hostnames.length ?? 0} 条 hostname 立即从日常解密里拿掉。要恢复，用同一个来源重新注册。
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>取消</AlertDialogCancel>
          <AlertDialogAction variant="danger" onClick={() => module && remove.mutate([module.id])}>
            移除模块
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
