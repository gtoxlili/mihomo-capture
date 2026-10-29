import { useEffect, useRef, useState, useSyncExternalStore } from "react"

export function useNow(intervalMs = 1000) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), intervalMs)
    return () => clearInterval(t)
  }, [intervalMs])
  return now
}

// Calls commit once typing pauses; the latest value wins.
export function useDebouncedCommit<T>(commit: (v: T) => void, ms = 300) {
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)
  useEffect(() => () => clearTimeout(timer.current), [])
  return (v: T, delay = ms) => {
    clearTimeout(timer.current)
    timer.current = setTimeout(() => commit(v), delay)
  }
}

export async function copyText(text: string) {
  await navigator.clipboard.writeText(text)
}

export function useMediaQuery(query: string) {
  return useSyncExternalStore(
    (onChange) => {
      const m = window.matchMedia(query)
      m.addEventListener("change", onChange)
      return () => m.removeEventListener("change", onChange)
    },
    () => window.matchMedia(query).matches
  )
}
