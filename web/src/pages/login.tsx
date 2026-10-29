import { useState } from "react"
import { CopyIcon, DownloadIcon } from "lucide-react"
import { toast } from "sonner"

import { BrandMark } from "@/components/brand"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { api, ApiError, token } from "@/lib/api"
import { copyText } from "@/lib/hooks"

const tokenCommand = "cat data/api-token"

export function LoginPage() {
  const [value, setValue] = useState("")
  const [remember, setRemember] = useState(true)
  const [error, setError] = useState("")
  const [pending, setPending] = useState(false)

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    const candidate = value.trim()
    if (!candidate) return
    setPending(true)
    setError("")
    try {
      await api.verify(candidate)
      token.set(candidate, remember)
    } catch (err) {
      setError(
        err instanceof ApiError && err.status === 401
          ? "令牌不对。确认复制的是 API 令牌，不是代理节点令牌。"
          : `连不上服务：${err instanceof Error ? err.message : String(err)}`
      )
    } finally {
      setPending(false)
    }
  }

  return (
    <div className="flex min-h-svh items-center justify-center px-4 py-10">
      <div className="w-full max-w-[26rem]">
        <div className="mb-8 flex items-center gap-3">
          <BrandMark className="size-9" />
          <div>
            <h1 className="text-xl font-semibold tracking-tight">Mihomo Capture</h1>
            <p className="text-[13px] text-muted-foreground">通过 mihomo 抓取本机 HTTP 与 HTTPS</p>
          </div>
        </div>
        <form
          onSubmit={submit}
          className="space-y-4 rounded-xl border bg-card p-6 shadow-[0_1px_2px_rgb(16_24_40/0.05),0_12px_32px_-16px_rgb(16_24_40/0.14)]"
        >
          <div className="space-y-2">
            <Label htmlFor="token">API 令牌</Label>
            <Input
              id="token"
              type="password"
              autoComplete="current-password"
              autoFocus
              value={value}
              onChange={(e) => setValue(e.target.value)}
              aria-invalid={!!error}
              className="h-9 font-mono"
            />
            {error && <p className="text-[13px] text-destructive">{error}</p>}
          </div>
          <div className="flex items-center gap-2">
            <Checkbox id="remember" checked={remember} onCheckedChange={(v) => setRemember(v === true)} />
            <Label htmlFor="remember" className="font-normal">
              在这个浏览器上记住
            </Label>
          </div>
          <Button type="submit" className="h-9 w-full" disabled={pending || !value.trim()}>
            {pending ? "正在验证…" : "进入控制台"}
          </Button>
        </form>
        <div className="mt-6 space-y-2 text-[13px] text-muted-foreground">
          <p>令牌由 -init 生成。从配置中的 api_token_file 读取。示例配置使用：</p>
          <button
            type="button"
            onClick={() => copyText(tokenCommand).then(() => toast.success("已复制命令"))}
            className="group flex w-full items-center gap-2 rounded-md border bg-card px-3 py-2 text-left font-mono text-xs text-foreground hover:border-input"
          >
            <span className="min-w-0 flex-1 truncate">{tokenCommand}</span>
            <CopyIcon className="size-3.5 shrink-0 text-muted-foreground group-hover:text-foreground" />
          </button>
          <p className="pt-2">
            给设备装根证书不需要令牌：
            <a href="/ca.crt" download className="ml-1 inline-flex items-center gap-1 text-signal hover:underline">
              <DownloadIcon className="size-3.5" />
              下载 Mihomo Capture CA
            </a>
          </p>
        </div>
      </div>
    </div>
  )
}
