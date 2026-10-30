import { ChevronDownIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import type { Device } from "@/lib/api"
import { deviceName } from "@/lib/format"
import { cn } from "@/lib/utils"

type Props = {
  selected: string[]
  devices: Device[]
  disabled?: boolean
  onChange: (devices: string[]) => void
}

// Trusted devices can be chosen; a listed device that lost trust stays visible
// so it can be removed.
export function DevicePicker({ selected, devices, disabled, onChange }: Props) {
  const shown = devices
    .filter((d) => d.trusted || selected.includes(d.id))
    .sort((a, b) => Number(b.trusted) - Number(a.trusted) || deviceName(a).localeCompare(deviceName(b), "zh-CN"))
  const names = selected.map((id) => {
    const d = devices.find((x) => x.id === id)
    return d ? deviceName(d) : id
  })
  const toggle = (id: string, on: boolean) => onChange(on ? [...selected, id] : selected.filter((x) => x !== id))
  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button variant="outline" size="sm" disabled={disabled} className="max-w-full font-normal">
          <span className="truncate">{names.length > 0 ? names.join("、") : "选择设备"}</span>
          <ChevronDownIcon className="text-muted-foreground" />
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-72 p-1.5">
        {shown.length === 0 ? (
          <p className="px-2 py-3 text-[13px] text-muted-foreground">还没有受信设备。先在设备页确认信任根证书。</p>
        ) : (
          <ul className="max-h-72 overflow-auto">
            {shown.map((d) => (
              <li key={d.id}>
                <label className="flex cursor-pointer items-start gap-2.5 rounded-md px-2 py-1.5 text-[13px] hover:bg-subtle">
                  <Checkbox
                    checked={selected.includes(d.id)}
                    onCheckedChange={(v) => toggle(d.id, v === true)}
                    className="mt-0.5"
                  />
                  <span className="min-w-0">
                    <span className={cn("block truncate", !d.trusted && "text-muted-foreground line-through")}>
                      {deviceName(d)}
                    </span>
                    <span className="block font-mono text-[11px] text-muted-foreground">
                      {d.trusted ? (d.online ? d.ips.join(", ") || d.id : "离线") : "未受信，不生效"}
                    </span>
                  </span>
                </label>
              </li>
            ))}
          </ul>
        )}
      </PopoverContent>
    </Popover>
  )
}
