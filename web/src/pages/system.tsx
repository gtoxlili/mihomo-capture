import { useState } from "react"
import { CheckIcon, TriangleAlertIcon } from "lucide-react"

import { ClearRecordsDialog } from "@/components/clear-records"
import { Button } from "@/components/ui/button"
import { token } from "@/lib/api"
import { formatBytes, formatDateTime } from "@/lib/format"
import { errorMessage, useStatus } from "@/lib/queries"
import { cn } from "@/lib/utils"

export function SystemPage() {
  const status = useStatus()
  const [clearing, setClearing] = useState(false)
  const s = status.data
  if (status.error)
    return <p className="p-6 text-[13px] text-destructive">读取状态失败：{errorMessage(status.error)}</p>
  if (!s) return null
  const g = s.gateway
  return (
    <div className="mx-auto max-w-4xl space-y-8 px-4 py-6">
      <h1 className="text-xl font-semibold tracking-tight">系统</h1>

      <Section title="网关">
        <Row label="同步">
          {s.gateway_error ? (
            <State bad>
              上一次和网关同步失败：<span className="font-mono">{s.gateway_error}</span>。服务每 5
              秒重试，恢复之前开始抓包会被拒绝。
            </State>
          ) : (
            <State>正常</State>
          )}
        </Row>
        {g && (
          <>
            <Row label="mihomo 版本">
              <span className="font-mono">{g.version}</span>
            </Row>
            <Row label="抓包通道">
              {g.catalog ? "已装进网关的运行配置" : "未装"}
              <p className="text-xs text-muted-foreground">
                没有受信设备也没有抓包时不装是常态；需要时服务自动装上。网关重载自己的配置后通道消失，服务下一次同步时补回。
              </p>
            </Row>
            <Row label="选择规则">
              <span className="tabular">
                抓包 {g.rules?.capture ?? 0} 条，日常 {dailyRules(g.rules)} 条
              </span>
              <p className="text-xs text-muted-foreground">
                启用的模块按设备分组，每组设备走一条日常通道；通道数按需翻倍增加，增加时网关重载一次配置。
              </p>
            </Row>
          </>
        )}
      </Section>

      <Section title="记录存储">
        <Row label="条数">
          <Meter value={s.storage.records} max={s.storage.max_records} format={(n) => `${n}`} />
        </Row>
        <Row label="占用">
          <Meter value={s.storage.bytes} max={s.storage.max_bytes} format={formatBytes} />
        </Row>
        <Row label="保留">{s.storage.retention_hours} 小时。超出时间、条数或容量时最旧的记录先被删除。</Row>
        <Row label="写入">{s.storage.last_error ? <State bad>{s.storage.last_error}</State> : <State>正常</State>}</Row>
        <Row label="清空">
          <Button variant="outline" size="sm" className="text-destructive" onClick={() => setClearing(true)}>
            清空全部记录…
          </Button>
        </Row>
      </Section>

      <Section title="根证书">
        <Row label="名称">{s.ca.name}</Row>
        <Row label="有效期至">{formatDateTime(s.ca.not_after)}</Row>
        <Row label="指纹">
          <span className="font-mono text-xs break-all">{s.ca.fingerprint}</span>
        </Row>
      </Section>

      <Section title="这个浏览器">
        <Row label="令牌">
          <div className="flex flex-wrap items-center gap-3">
            <span className="text-muted-foreground">保存在这个浏览器里，只用来访问 API。</span>
            <Button variant="outline" size="sm" onClick={() => token.clear()}>
              退出
            </Button>
          </div>
        </Row>
        <Row label="API 版本">
          <span className="font-mono">v{s.api_version}</span>
        </Row>
      </Section>
      <ClearRecordsDialog open={clearing} onOpenChange={setClearing} />
    </div>
  )
}

// Daily lanes are named daily-0, daily-1, … after the slot each module set takes.
function dailyRules(rules: Record<string, number> | null) {
  return Object.entries(rules ?? {})
    .filter(([lane]) => lane.startsWith("daily-"))
    .reduce((n, [, count]) => n + count, 0)
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section>
      <h2 className="mb-2 text-[15px] font-semibold">{title}</h2>
      <dl className="divide-y divide-line rounded-xl border bg-card lift">{children}</dl>
    </section>
  )
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid gap-1 px-4 py-3 text-[13px] sm:grid-cols-[8rem_minmax(0,1fr)] sm:gap-4">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0">{children}</dd>
    </div>
  )
}

function State({ bad, children }: { bad?: boolean; children: React.ReactNode }) {
  return (
    <span className={cn("flex items-start gap-1.5", bad ? "text-warn-ink" : "text-good")}>
      {bad ? (
        <TriangleAlertIcon className="mt-0.5 size-3.5 shrink-0" />
      ) : (
        <CheckIcon className="mt-0.5 size-3.5 shrink-0" />
      )}
      <span className="min-w-0 break-words">{children}</span>
    </span>
  )
}

function Meter({ value, max, format }: { value: number; max: number; format: (n: number) => string }) {
  const ratio = max > 0 ? Math.min(1, value / max) : 0
  return (
    <div className="flex items-center gap-3">
      <div
        role="meter"
        aria-valuenow={value}
        aria-valuemin={0}
        aria-valuemax={max}
        className="h-1.5 w-40 overflow-hidden rounded-full bg-secondary"
      >
        <div
          className="h-full rounded-full bg-ink-2"
          style={{ width: `${Math.max(ratio * 100, value > 0 ? 2 : 0)}%` }}
        />
      </div>
      <span className="tabular">
        {format(value)} / {format(max)}
      </span>
    </div>
  )
}
