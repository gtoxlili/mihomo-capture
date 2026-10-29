import { lazy, Suspense, useState } from "react"
import { CopyIcon, DownloadIcon } from "lucide-react"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import type { Body } from "@/lib/api"
import { bodyBytes, prettyJSON, viewBody } from "@/lib/body"
import { formatBytes } from "@/lib/format"
import { copyText } from "@/lib/hooks"
import { HexView } from "./hex-view"
import { KeyValues } from "./key-values"

const CodeView = lazy(() => import("./code-view"))

type Props = {
  title: string
  body: Body
  contentType: string
  name: string
  pending?: boolean
}

export function BodyPanel({ title, body, contentType, name, pending }: Props) {
  const [decoded, setDecoded] = useState(true)
  const [pretty, setPretty] = useState(true)
  const shown = decoded && body.decoded ? body.decoded : body
  const view = viewBody(body, contentType, decoded)
  const text = view.kind === "text" ? (view.language === "json" && pretty ? prettyJSON(view.text) : view.text) : ""

  function download() {
    const blob = new Blob([bodyBytes(shown) as BlobPart], { type: contentType || "application/octet-stream" })
    const url = URL.createObjectURL(blob)
    const a = document.createElement("a")
    a.href = url
    a.download = name
    a.click()
    URL.revokeObjectURL(url)
  }

  return (
    <section className="space-y-2">
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
        <h3 className="text-[13px] font-semibold">{title}</h3>
        <span className="text-xs text-muted-foreground tabular">
          {formatBytes(body.size)}
          {contentType && <span className="ml-2 font-mono">{contentType.split(";")[0]}</span>}
        </span>
        <div className="ml-auto flex items-center gap-1">
          {body.decoded && (
            <ToggleGroup
              type="single"
              size="sm"
              variant="segmented"
              value={decoded ? "decoded" : "wire"}
              onValueChange={(v) => v && setDecoded(v === "decoded")}
            >
              <ToggleGroupItem value="decoded" className="h-6 px-2 text-xs">
                解压后
              </ToggleGroupItem>
              <ToggleGroupItem value="wire" className="h-6 px-2 text-xs">
                {body.content_encoding} 原文
              </ToggleGroupItem>
            </ToggleGroup>
          )}
          {view.kind === "text" && view.language === "json" && (
            <Button variant="ghost" size="xs" className="text-muted-foreground" onClick={() => setPretty(!pretty)}>
              {pretty ? "显示原文" : "格式化"}
            </Button>
          )}
          {(view.kind === "text" || view.kind === "form") && (
            <Button
              variant="ghost"
              size="xs"
              className="text-muted-foreground"
              onClick={() => copyText(view.kind === "text" ? text : view.text).then(() => toast.success("已复制正文"))}
            >
              <CopyIcon />
              复制
            </Button>
          )}
          {view.kind !== "empty" && (
            <Button variant="ghost" size="xs" className="text-muted-foreground" onClick={download}>
              <DownloadIcon />
              下载
            </Button>
          )}
        </div>
      </div>
      {(body.truncated || shown.truncated) && (
        <p className="rounded-md bg-subtle px-3 py-1.5 text-[13px] text-muted-foreground">
          {body.truncated
            ? `只记下了前 ${formatBytes(bodyBytes(body).length)}，完整的 ${formatBytes(body.size)} 照常转发给了对方。`
            : "解压后的内容超出上限，只显示前一部分。"}
        </p>
      )}
      {view.kind === "empty" ? (
        <p className="text-[13px] text-muted-foreground">{pending ? "正文还在传输。" : "没有正文"}</p>
      ) : view.kind === "image" ? (
        <div className="flex justify-center rounded-md border bg-[repeating-conic-gradient(#f2f4f7_0_25%,#fff_0_50%)] bg-[length:16px_16px] p-4">
          <img src={view.src} alt={name} className="max-h-[60vh] max-w-full object-contain" />
        </div>
      ) : view.kind === "binary" ? (
        <HexView bytes={view.bytes} />
      ) : view.kind === "form" ? (
        <KeyValues title="表单字段" entries={view.fields} />
      ) : (
        <Suspense fallback={<pre className="h-24 rounded-md border bg-canvas" />}>
          <CodeView text={text} language={view.language} />
        </Suspense>
      )}
    </section>
  )
}
