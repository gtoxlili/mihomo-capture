import { CopyIcon } from "lucide-react"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import { copyText } from "@/lib/hooks"

export function KeyValues({ title, entries }: { title: string; entries: [string, string][] }) {
  return (
    <section className="space-y-2">
      <div className="flex items-center gap-2">
        <h3 className="text-[13px] font-semibold">{title}</h3>
        <span className="text-xs text-muted-foreground tabular">{entries.length}</span>
        {entries.length > 0 && (
          <Button
            variant="ghost"
            size="xs"
            className="ml-auto text-muted-foreground"
            onClick={() =>
              copyText(entries.map(([k, v]) => `${k}: ${v}`).join("\n")).then(() => toast.success(`已复制${title}`))
            }
          >
            <CopyIcon />
            复制
          </Button>
        )}
      </div>
      {entries.length === 0 ? (
        <p className="text-[13px] text-muted-foreground">没有</p>
      ) : (
        <dl className="grid grid-cols-[minmax(7rem,max-content)_minmax(0,1fr)] overflow-hidden rounded-md border border-line font-mono text-xs">
          {entries.map(([k, v], i) => (
            <div
              key={`${k}-${i}`}
              className="contents [&>*]:border-b [&>*]:border-line [&>*]:px-3 [&>*]:py-1.5 last:[&>*]:border-b-0"
            >
              <dt className="bg-subtle/70 break-all text-muted-foreground">{k}</dt>
              <dd className="break-all whitespace-pre-wrap text-ink select-text">{v}</dd>
            </div>
          ))}
        </dl>
      )}
    </section>
  )
}
