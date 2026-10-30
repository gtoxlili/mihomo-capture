import { lazy, Suspense, useState } from "react"
import { Link, useNavigate, useParams } from "react-router"
import { ArrowLeftIcon, PlusIcon, XIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import type { BodyRewrite, MapLocal, ModuleDefinition, Script, URLRewrite } from "@/lib/api"
import { errorMessage, useModule, useSaveModule } from "@/lib/queries"
import { cn } from "@/lib/utils"

const CodeEditor = lazy(() => import("./code-editor"))

// The form keeps the two free-text fields as text; everything else is the
// definition the API takes.
type Draft = Omit<ModuleDefinition, "hostnames" | "map_locals"> & {
  hostnames: string
  map_locals: (Omit<MapLocal, "headers"> & { headers: string })[]
}

const blank: ModuleDefinition = {
  name: "",
  hostnames: [],
  header_rewrites: [],
  url_rewrites: [],
  map_locals: [],
  body_rewrites: [],
  scripts: [],
}

// Only definition fields: the API rejects any other field, such as a loaded
// module's id or activation.
function toDraft(d: ModuleDefinition): Draft {
  return {
    name: d.name,
    header_rewrites: d.header_rewrites,
    url_rewrites: d.url_rewrites,
    body_rewrites: d.body_rewrites,
    scripts: d.scripts,
    hostnames: d.hostnames.join("\n"),
    map_locals: d.map_locals.map((l) => ({
      ...l,
      headers: Object.entries(l.headers ?? {})
        .map(([k, v]) => `${k}: ${v}`)
        .join("\n"),
    })),
  }
}

function fromDraft(d: Draft): ModuleDefinition {
  return {
    ...d,
    hostnames: d.hostnames
      .split(/[\n,]/)
      .map((h) => h.trim())
      .filter(Boolean),
    map_locals: d.map_locals.map((l) => ({
      ...l,
      headers: Object.fromEntries(
        l.headers
          .split("\n")
          .map((line) => line.split(/:(.*)/s).map((s) => s.trim()))
          .filter(([k]) => k)
          .map(([k, v]) => [k, v ?? ""])
      ),
    })),
  }
}

export function ModuleEditorPage() {
  const { id } = useParams()
  const q = useModule(id)
  if (id && !q.data) {
    return (
      <p className={cn("p-6 text-[13px] text-muted-foreground", q.error && "text-destructive")}>
        {q.error ? `读取模块失败：${errorMessage(q.error)}` : "正在读取模块…"}
      </p>
    )
  }
  return <Editor key={id ?? "new"} id={id} initial={q.data ?? blank} />
}

function Editor({ id, initial }: { id?: string; initial: ModuleDefinition }) {
  const [d, setD] = useState<Draft>(() => toDraft(initial))
  const save = useSaveModule()
  const navigate = useNavigate()
  function list<K extends keyof Draft>(key: K) {
    const items = d[key] as unknown[]
    return {
      add: (item: unknown) => setD({ ...d, [key]: [...items, item] }),
      remove: (i: number) => setD({ ...d, [key]: items.filter((_, j) => j !== i) }),
      update: (i: number, patch: object) =>
        setD({ ...d, [key]: items.map((x, j) => (j === i ? { ...(x as object), ...patch } : x)) }),
    }
  }
  const headers = list("header_rewrites")
  const urls = list("url_rewrites")
  const locals = list("map_locals")
  const bodies = list("body_rewrites")
  const scripts = list("scripts")

  return (
    <form
      className="mx-auto max-w-5xl space-y-6 px-4 py-6"
      onSubmit={(e) => {
        e.preventDefault()
        save.mutate([id, fromDraft(d)], { onSuccess: () => navigate("/modules") })
      }}
    >
      <div className="flex items-center gap-2">
        <Button variant="ghost" size="icon-sm" asChild>
          <Link to="/modules" aria-label="返回模块列表">
            <ArrowLeftIcon />
          </Link>
        </Button>
        <h1 className="text-xl font-semibold tracking-tight">{id ? "编辑模块" : "新建模块"}</h1>
      </div>

      <Section title="名称与主机名">
        <div className="grid gap-4 sm:grid-cols-[16rem_minmax(0,1fr)]">
          <div className="space-y-1.5">
            <Label htmlFor="name">名称</Label>
            <Input id="name" value={d.name} onChange={(e) => setD({ ...d, name: e.target.value })} className="h-8" />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="hostnames">主机名</Label>
            <Textarea
              id="hostnames"
              value={d.hostnames}
              onChange={(e) => setD({ ...d, hostnames: e.target.value })}
              placeholder={"-*.apple.com\n*.example.com\napi.example.org"}
              className="min-h-24 font-mono text-xs"
            />
            <p className="text-xs leading-relaxed text-muted-foreground">
              每行一个：* 与 ? 通配，前缀 -
              表示排除，从上往下第一个匹配的条目决定，所以排除要写在前面。条目覆盖这个主机的所有端口。只有这些主机的请求会被解密并交给下面的规则。
            </p>
          </div>
        </div>
      </Section>

      <Section
        title="Header 改写"
        hint="每条匹配的规则依次生效。URL 正则匹配完整 URL。header-add 追加一行，header-del 删除，header-replace 只替换已有的值，header-replace-regex 用正则和模板替换已有的值（$1 引用分组）。"
        onAdd={() => headers.add({ type: "http-request", pattern: "", action: "header-add", name: "", value: "" })}
      >
        {d.header_rewrites.map((h, i) => (
          <Row key={i} onRemove={() => headers.remove(i)}>
            <Choice
              value={h.type}
              onChange={(type) => headers.update(i, { type })}
              options={directions}
              className="w-24"
            />
            <Field label="URL 正则" value={h.pattern} onChange={(pattern) => headers.update(i, { pattern })} wide />
            <Choice
              value={h.action}
              onChange={(action) => headers.update(i, { action })}
              options={headerActions}
              className="w-44"
            />
            <Field label="Header 名称" value={h.name} onChange={(name) => headers.update(i, { name })} />
            {h.action === "header-replace-regex" ? (
              <>
                <Field label="正则" value={h.regex ?? ""} onChange={(regex) => headers.update(i, { regex })} />
                <Field
                  label="替换模板"
                  value={h.replacement ?? ""}
                  onChange={(replacement) => headers.update(i, { replacement })}
                />
              </>
            ) : h.action !== "header-del" ? (
              <Field label="值" value={h.value ?? ""} onChange={(value) => headers.update(i, { value })} />
            ) : null}
          </Row>
        ))}
      </Section>

      <Section
        title="URL 改写"
        hint="第一条匹配的规则决定。header 原地改写请求并更新 Host，客户端无感；302、307 让客户端重定向到替换结果；reject 拒绝请求，返回 404。替换结果用 $1 引用分组。"
        onAdd={() => urls.add({ pattern: "", replacement: "", type: "header" })}
      >
        {d.url_rewrites.map((u: URLRewrite, i) => (
          <Row key={i} onRemove={() => urls.remove(i)}>
            <Field label="URL 正则" value={u.pattern} onChange={(pattern) => urls.update(i, { pattern })} wide />
            {u.type !== "reject" && (
              <Field
                label="替换为"
                value={u.replacement}
                onChange={(replacement) => urls.update(i, { replacement })}
                wide
              />
            )}
            <Choice value={u.type} onChange={(type) => urls.update(i, { type })} options={urlTypes} className="w-28" />
          </Row>
        ))}
      </Section>

      <Section
        title="本地映射"
        hint="第一条匹配的规则直接返回本地构造的响应，不访问上游。text 原样返回数据，base64 返回解码后的二进制，tiny-gif 返回 1 像素 GIF。默认 Content-Type 依次是 text/plain、application/octet-stream、image/gif，可用响应头覆盖。"
        onAdd={() => locals.add({ pattern: "", data_type: "text", data: "", status_code: 200, headers: "" })}
      >
        {d.map_locals.map((l, i) => (
          <Row key={i} onRemove={() => locals.remove(i)}>
            <Field label="URL 正则" value={l.pattern} onChange={(pattern) => locals.update(i, { pattern })} wide />
            <Choice
              value={l.data_type}
              onChange={(data_type) => locals.update(i, { data_type })}
              options={dataTypes}
              className="w-28"
            />
            <Field
              label="状态码"
              value={String(l.status_code)}
              onChange={(v) => locals.update(i, { status_code: Number(v) || 0 })}
              className="w-20"
              inputMode="numeric"
            />
            {l.data_type !== "tiny-gif" && (
              <Area label="数据" value={l.data} onChange={(data) => locals.update(i, { data })} />
            )}
            <Area
              label="响应头，每行「名称: 值」"
              value={l.headers}
              onChange={(headers) => locals.update(i, { headers })}
            />
          </Row>
        ))}
      </Section>

      <Section
        title="Body 改写"
        hint="每条匹配的规则依次生效，在脚本之前。正则方式要求正文是 UTF-8 文本，^ 和 $ 匹配每一行，正则对按顺序替换；jq 方式要求正文是 JSON，过滤器没有输出时保留原文。压缩的正文先解压，改写后以未压缩形式发送。超过 10 MiB 的正文原样放行。"
        onAdd={() => bodies.add({ type: "http-response", pattern: "", replacements: [{ regex: "", replacement: "" }] })}
      >
        {d.body_rewrites.map((b: BodyRewrite, i) => {
          const jq = b.type.endsWith("-jq")
          return (
            <Row key={i} onRemove={() => bodies.remove(i)}>
              <Choice
                value={b.type}
                onChange={(type) =>
                  bodies.update(
                    i,
                    type.endsWith("-jq")
                      ? { type, jq: b.jq ?? "", replacements: undefined }
                      : { type, jq: undefined, replacements: b.replacements ?? [{ regex: "", replacement: "" }] }
                  )
                }
                options={bodyTypes}
                className="w-36"
              />
              <Field label="URL 正则" value={b.pattern} onChange={(pattern) => bodies.update(i, { pattern })} wide />
              {jq ? (
                <Area label="jq 过滤器" value={b.jq ?? ""} onChange={(v) => bodies.update(i, { jq: v })} />
              ) : (
                <div className="w-full space-y-1.5">
                  {(b.replacements ?? []).map((p, j) => (
                    <div key={j} className="flex items-end gap-2">
                      <Field
                        label="正文正则"
                        value={p.regex}
                        onChange={(regex) =>
                          bodies.update(i, {
                            replacements: b.replacements!.map((x, k) => (k === j ? { ...x, regex } : x)),
                          })
                        }
                        wide
                      />
                      <Field
                        label="替换为"
                        value={p.replacement}
                        onChange={(replacement) =>
                          bodies.update(i, {
                            replacements: b.replacements!.map((x, k) => (k === j ? { ...x, replacement } : x)),
                          })
                        }
                        wide
                      />
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-sm"
                        aria-label="删掉这一对"
                        disabled={b.replacements!.length === 1}
                        onClick={() => bodies.update(i, { replacements: b.replacements!.filter((_, k) => k !== j) })}
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
                    onClick={() =>
                      bodies.update(i, { replacements: [...(b.replacements ?? []), { regex: "", replacement: "" }] })
                    }
                  >
                    <PlusIcon />
                    再加一对
                  </Button>
                </div>
              )}
            </Row>
          )
        })}
      </Section>

      <Section
        title="脚本"
        hint="http-request 脚本处理请求，http-response 脚本处理响应，可用 $request、$response、$done、$argument、$persistentStore、$httpClient、$notification、$utils.ungzip、console、setTimeout、TextEncoder、TextDecoder，详见 API 文档。每个请求和每个响应只运行第一个匹配的脚本。出错、超时或没有调用 $done 时原样放行，原因记在日志里。"
        onAdd={() =>
          scripts.add({
            name: `script-${d.scripts.length + 1}`,
            type: "http-response",
            pattern: "",
            code: "$done({})\n",
            requires_body: false,
            binary_body_mode: false,
            full_header_mode: false,
            max_size: 10485760,
            timeout: 5,
            argument: "",
          })
        }
      >
        {d.scripts.map((s: Script, i) => (
          <div key={i} className="space-y-3 rounded-lg border bg-background p-3">
            <div className="flex flex-wrap items-end gap-2">
              <Field label="名称" value={s.name} onChange={(name) => scripts.update(i, { name })} />
              <Choice
                value={s.type}
                onChange={(type) => scripts.update(i, { type })}
                options={directions}
                className="w-24"
              />
              <Field label="URL 正则" value={s.pattern} onChange={(pattern) => scripts.update(i, { pattern })} wide />
              <Field
                label="超时（秒）"
                value={String(s.timeout)}
                onChange={(v) => scripts.update(i, { timeout: Number(v) || 0 })}
                className="w-20"
                inputMode="numeric"
              />
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label="删除这个脚本"
                onClick={() => scripts.remove(i)}
                className="ml-auto"
              >
                <XIcon />
              </Button>
            </div>
            <div className="flex flex-wrap items-center gap-x-5 gap-y-2 text-[13px]">
              <Flag
                label="requires-body：传入并允许替换正文"
                checked={s.requires_body}
                onChange={(requires_body) => scripts.update(i, { requires_body })}
              />
              <Flag
                label="binary-body-mode：正文为 Uint8Array"
                checked={s.binary_body_mode}
                onChange={(binary_body_mode) => scripts.update(i, { binary_body_mode })}
              />
              <Flag
                label="full-header-mode：头为 {field, value} 数组"
                checked={s.full_header_mode}
                onChange={(full_header_mode) => scripts.update(i, { full_header_mode })}
              />
            </div>
            <div className="flex flex-wrap items-end gap-2">
              <Field
                label="max-size（字节，-1 为 32 MiB）"
                value={String(s.max_size)}
                onChange={(v) => scripts.update(i, { max_size: Number(v) || 0 })}
                className="w-56"
                inputMode="numeric"
              />
              <Field
                label="argument（$argument）"
                value={s.argument ?? ""}
                onChange={(argument) => scripts.update(i, { argument })}
                wide
              />
            </div>
            <Suspense
              fallback={
                <Textarea value={s.code} readOnly className="min-h-40 font-mono text-xs" aria-label="脚本代码" />
              }
            >
              <CodeEditor value={s.code} onChange={(code) => scripts.update(i, { code })} />
            </Suspense>
          </div>
        ))}
      </Section>

      <div className="sticky bottom-0 -mx-4 flex flex-wrap items-center gap-3 border-t bg-card/95 px-4 py-3 backdrop-blur">
        {save.error && (
          <p className="min-w-0 flex-1 text-[13px] break-words text-destructive">{errorMessage(save.error)}</p>
        )}
        <div className="ml-auto flex gap-2">
          <Button type="button" variant="outline" asChild>
            <Link to="/modules">取消</Link>
          </Button>
          <Button type="submit" disabled={save.isPending || !d.name.trim()}>
            保存
          </Button>
        </div>
      </div>
      {!id && <p className="text-xs text-muted-foreground">新模块保存后处于停用状态，在模块列表里启用并选择设备。</p>}
    </form>
  )
}

const directions = [
  { value: "http-request", label: "请求" },
  { value: "http-response", label: "响应" },
]
const headerActions = ["header-add", "header-del", "header-replace", "header-replace-regex"].map((v) => ({
  value: v,
  label: v,
}))
const urlTypes = ["header", "302", "307", "reject"].map((v) => ({ value: v, label: v }))
const dataTypes = ["text", "base64", "tiny-gif"].map((v) => ({ value: v, label: v }))
const bodyTypes = [
  { value: "http-request", label: "请求 · 正则" },
  { value: "http-response", label: "响应 · 正则" },
  { value: "http-request-jq", label: "请求 · jq" },
  { value: "http-response-jq", label: "响应 · jq" },
]

function Section({
  title,
  hint,
  onAdd,
  children,
}: {
  title: string
  hint?: string
  onAdd?: () => void
  children: React.ReactNode
}) {
  return (
    <section className="space-y-3 rounded-xl border bg-card p-4 lift">
      <div className="flex items-start gap-3">
        <div className="min-w-0 flex-1 space-y-1">
          <h2 className="text-[15px] font-semibold">{title}</h2>
          {hint && <p className="text-xs leading-relaxed text-muted-foreground">{hint}</p>}
        </div>
        {onAdd && (
          <Button type="button" variant="outline" size="sm" onClick={onAdd}>
            <PlusIcon />
            添加
          </Button>
        )}
      </div>
      {children}
    </section>
  )
}

function Row({ onRemove, children }: { onRemove: () => void; children: React.ReactNode }) {
  return (
    <div className="flex flex-wrap items-end gap-2 rounded-lg border bg-background p-3">
      {children}
      <Button
        type="button"
        variant="ghost"
        size="icon-sm"
        aria-label="删除这条规则"
        onClick={onRemove}
        className="ml-auto"
      >
        <XIcon />
      </Button>
    </div>
  )
}

function Field({
  label,
  value,
  onChange,
  wide,
  className,
  inputMode,
}: {
  label: string
  value: string
  onChange: (v: string) => void
  wide?: boolean
  className?: string
  inputMode?: "numeric"
}) {
  return (
    <label className={cn("space-y-1", wide ? "min-w-48 flex-1" : "w-40", className)}>
      <span className="block text-xs text-muted-foreground">{label}</span>
      <Input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        inputMode={inputMode}
        className="h-8 font-mono text-xs"
      />
    </label>
  )
}

function Area({ label, value, onChange }: { label: string; value: string; onChange: (v: string) => void }) {
  return (
    <label className="w-full space-y-1">
      <span className="block text-xs text-muted-foreground">{label}</span>
      <Textarea value={value} onChange={(e) => onChange(e.target.value)} className="min-h-16 font-mono text-xs" />
    </label>
  )
}

function Choice<T extends string>({
  value,
  onChange,
  options,
  className,
}: {
  value: T
  onChange: (v: T) => void
  options: { value: string; label: string }[]
  className?: string
}) {
  return (
    <Select value={value} onValueChange={(v) => onChange(v as T)}>
      <SelectTrigger size="sm" className={cn("font-mono text-xs", className)}>
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {options.map((o) => (
          <SelectItem key={o.value} value={o.value} className="text-xs">
            {o.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

function Flag({ label, checked, onChange }: { label: string; checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <label className="flex cursor-pointer items-center gap-2">
      <Checkbox checked={checked} onCheckedChange={(v) => onChange(v === true)} />
      <span className="font-mono text-xs">{label}</span>
    </label>
  )
}
