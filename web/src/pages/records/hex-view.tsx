import { useRef } from "react"
import { useVirtualizer } from "@tanstack/react-virtual"

const width = 16

// Offset, sixteen bytes in hex and their printable ASCII, one row per line.
export function HexView({ bytes }: { bytes: Uint8Array }) {
  const scroller = useRef<HTMLDivElement>(null)
  const rows = Math.ceil(bytes.length / width)
  // React Compiler leaves this component unmemoized: the virtualizer returns new functions each render.
  // eslint-disable-next-line react-hooks/incompatible-library
  const v = useVirtualizer({
    count: rows,
    getScrollElement: () => scroller.current,
    estimateSize: () => 20,
    overscan: 20,
  })
  return (
    <div
      ref={scroller}
      tabIndex={0}
      aria-label="十六进制视图"
      className="max-h-[60vh] overflow-auto rounded-md border bg-background font-mono text-xs leading-5 outline-none select-text focus-visible:ring-2 focus-visible:ring-ring/40"
    >
      <div style={{ height: v.getTotalSize(), position: "relative" }} className="min-w-max">
        {v.getVirtualItems().map((item) => {
          const start = item.index * width
          const line = bytes.subarray(start, start + width)
          const hex = Array.from(line, (b) => b.toString(16).padStart(2, "0"))
          return (
            <div
              key={item.key}
              className="absolute inset-x-0 flex gap-4 px-3 whitespace-pre"
              style={{ transform: `translateY(${item.start}px)`, height: 20 }}
            >
              <span className="text-muted-foreground/70">{start.toString(16).padStart(8, "0")}</span>
              <span>
                {hex.slice(0, 8).join(" ").padEnd(23)}
                {"  "}
                {hex.slice(8).join(" ").padEnd(23)}
              </span>
              <span className="text-muted-foreground">
                {Array.from(line, (b) => (b >= 0x20 && b < 0x7f ? String.fromCharCode(b) : "·")).join("")}
              </span>
            </div>
          )
        })}
      </div>
    </div>
  )
}
