import { cn } from "@/lib/utils"

// A tap on the line: the ring is the wire, the dot is the device being heard.
export function BrandMark({ className, live }: { className?: string; live?: boolean }) {
  return (
    <svg viewBox="0 0 32 32" aria-hidden className={cn("size-6", className)}>
      <rect width="32" height="32" rx="8" className="fill-ink" />
      <circle cx="16" cy="16" r="8.5" fill="none" stroke="white" strokeOpacity="0.9" strokeWidth="2.2" />
      <circle cx="16" cy="16" r="3.4" className={live ? "fill-tally" : "fill-white/60"} />
    </svg>
  )
}
