import { NavLink, Outlet } from "react-router"
import {
  BadgeCheckIcon,
  GaugeIcon,
  ListIcon,
  LogOutIcon,
  PuzzleIcon,
  SmartphoneIcon,
  TriangleAlertIcon,
} from "lucide-react"

import { BrandMark } from "@/components/brand"
import { TapBand } from "@/components/tap-band"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { token } from "@/lib/api"
import { useStatus } from "@/lib/queries"
import { cn } from "@/lib/utils"

const nav = [
  { to: "/", label: "记录", icon: ListIcon, end: true },
  { to: "/devices", label: "设备", icon: SmartphoneIcon },
  { to: "/modules", label: "模块", icon: PuzzleIcon },
  { to: "/certificate", label: "根证书", icon: BadgeCheckIcon },
  { to: "/system", label: "系统", icon: GaugeIcon },
]

export function Shell() {
  const status = useStatus()
  const s = status.data
  const live = !!s?.capture
  return (
    <div className="flex h-svh flex-col">
      <header className="flex h-12 shrink-0 items-center gap-1 border-b bg-card px-3 sm:gap-3 sm:px-5">
        <NavLink to="/" className="flex items-center gap-2.5" aria-label="Mihomo Capture">
          <BrandMark live={live} />
          <span className="hidden text-[15px] font-semibold tracking-tight md:inline">Mihomo Capture</span>
        </NavLink>
        <span aria-hidden className="mx-1 hidden h-5 w-px bg-line md:block" />
        <nav className="flex min-w-0 flex-1 items-center gap-0.5 overflow-x-auto">
          {nav.map(({ to, label, icon: Icon, end }) => (
            <NavLink
              key={to}
              to={to}
              end={end}
              className={({ isActive }) =>
                cn(
                  "flex h-8 shrink-0 items-center gap-1.5 rounded-md px-2.5 text-[13px] text-muted-foreground transition-colors hover:bg-subtle hover:text-foreground [&>svg]:text-faint",
                  isActive && "bg-subtle font-medium text-foreground [&>svg]:text-signal"
                )
              }
            >
              <Icon className="size-4" />
              <span className="hidden sm:inline">{label}</span>
            </NavLink>
          ))}
        </nav>
        {s && (s.gateway_error || s.storage.last_error) ? (
          <NavLink
            to="/system"
            className="flex h-8 items-center gap-1.5 rounded-md px-2 text-[13px] font-medium text-status-client hover:bg-muted"
          >
            <TriangleAlertIcon className="size-4" />
            <span className="hidden lg:inline">{s.gateway_error ? "网关同步失败" : "记录写入失败"}</span>
          </NavLink>
        ) : s?.gateway ? (
          <span className="hidden font-mono text-xs text-faint lg:inline">mihomo {s.gateway.version}</span>
        ) : null}
        <Tooltip>
          <TooltipTrigger asChild>
            <Button variant="ghost" size="icon-sm" onClick={() => token.clear()} aria-label="退出">
              <LogOutIcon />
            </Button>
          </TooltipTrigger>
          <TooltipContent>退出：这个浏览器不再保存令牌</TooltipContent>
        </Tooltip>
      </header>
      <TapBand />
      {s?.gateway_error ? (
        <Notice>
          网关同步失败：<span className="font-mono">{s.gateway_error}</span>
          。服务每 5 秒重试，恢复之前不能开始新的抓包。
        </Notice>
      ) : null}
      {s?.storage.last_error ? (
        <Notice>
          最近一次写入记录失败：<span className="font-mono">{s.storage.last_error}</span>
        </Notice>
      ) : null}
      <main className="min-h-0 flex-1 overflow-auto">
        <Outlet />
      </main>
    </div>
  )
}

function Notice({ children }: { children: React.ReactNode }) {
  return (
    <div
      role="alert"
      className="flex shrink-0 items-start gap-2 border-b border-warn-line bg-warn-wash px-5 py-2 text-[13px] text-warn-ink"
    >
      <TriangleAlertIcon className="mt-0.5 size-4 shrink-0" />
      <p className="min-w-0 break-words">{children}</p>
    </div>
  )
}
