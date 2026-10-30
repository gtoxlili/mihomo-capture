import { keepPreviousData, useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

import { api, ApiError, type ModuleDefinition, type RecordFilter } from "./api"

export const keys = {
  status: ["status"] as const,
  devices: ["devices"] as const,
  modules: ["modules"] as const,
  module: (id: string) => ["modules", id] as const,
  moduleLogs: (id: string) => ["module-logs", id] as const,
  records: (f: RecordFilter) => ["records", f] as const,
  record: (id: string) => ["record", id] as const,
  ticker: (device: string) => ["ticker", device] as const,
}

export function useStatus() {
  return useQuery({ queryKey: keys.status, queryFn: api.status, refetchInterval: 2000 })
}

export function useDevices() {
  return useQuery({
    queryKey: keys.devices,
    queryFn: async () => (await api.devices()).items,
    // The service rereads the neighbor table every 5 seconds.
    refetchInterval: 5000,
  })
}

export function useModules() {
  return useQuery({ queryKey: keys.modules, queryFn: async () => (await api.modules()).items, refetchInterval: 15000 })
}

export function useModule(id: string | undefined) {
  return useQuery({ queryKey: keys.module(id ?? ""), queryFn: () => api.module(id!), enabled: !!id })
}

export function useModuleLogs(id: string | null) {
  return useQuery({
    queryKey: keys.moduleLogs(id ?? ""),
    queryFn: async () => (await api.moduleLogs(id!)).items,
    enabled: !!id,
    refetchInterval: 3000,
  })
}

export function useRecords(filter: RecordFilter, live: boolean) {
  return useInfiniteQuery({
    queryKey: keys.records(filter),
    queryFn: ({ pageParam }) => api.records(filter, pageParam || undefined),
    initialPageParam: "",
    getNextPageParam: (last) => last.next || undefined,
    refetchInterval: live ? 2000 : false,
    placeholderData: keepPreviousData,
  })
}

export function useRecord(id: string | null) {
  return useQuery({
    queryKey: keys.record(id ?? ""),
    queryFn: () => api.record(id!),
    enabled: !!id,
    // Open streams and WebSockets keep adding to the record until complete.
    refetchInterval: (q) => (q.state.data && !q.state.data.complete ? 1500 : false),
  })
}

// The latest requests of the device being captured, for the tap band.
export function useTicker(device: string | undefined) {
  return useQuery({
    queryKey: keys.ticker(device ?? ""),
    queryFn: async () => (await api.records({ device }, undefined, 48)).items,
    enabled: !!device,
    refetchInterval: 2000,
  })
}

export function errorMessage(e: unknown) {
  if (e instanceof ApiError) return e.message
  if (e instanceof Error) return e.message
  return String(e)
}

function useAction<A extends unknown[]>(
  fn: (...args: A) => Promise<unknown>,
  opts: { success?: string; invalidate: readonly (readonly string[])[] }
) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (args: A) => fn(...args),
    onSuccess: () => {
      if (opts.success) toast.success(opts.success)
    },
    onError: (e) => toast.error(errorMessage(e)),
    onSettled: () => Promise.all(opts.invalidate.map((k) => qc.invalidateQueries({ queryKey: k }))),
  })
}

const everything = [keys.status, keys.devices, ["records"], ["ticker"]] as const

export function useStartCapture() {
  return useAction(
    async (deviceId: string, ports: number[], replacing: boolean) => {
      // The service runs one capture at a time; switching stops the old one first.
      if (replacing) await api.stopCapture()
      return api.startCapture(deviceId, ports)
    },
    { success: "已开始抓包", invalidate: everything }
  )
}

export function useStopCapture() {
  return useAction(() => api.stopCapture(), { success: "已停止抓包", invalidate: everything })
}

export function useSetTrust() {
  return useAction((id: string, trusted: boolean, evidence?: string) => api.setTrust(id, trusted, evidence), {
    invalidate: [keys.status, keys.devices],
  })
}

export function useRenameDevice() {
  return useAction((id: string, label: string) => api.renameDevice(id, label), {
    success: "已保存名称",
    invalidate: [keys.devices],
  })
}

export function useRefreshDevices() {
  return useAction(() => api.refreshDevices(), {
    success: "已刷新邻居表",
    invalidate: [keys.devices, keys.status],
  })
}

export function useSaveModule() {
  return useAction(
    (id: string | undefined, d: ModuleDefinition) => (id ? api.updateModule(id, d) : api.createModule(d)),
    {
      success: "已保存模块",
      invalidate: [keys.modules],
    }
  )
}

export function useActivateModule() {
  return useAction((id: string, enabled: boolean, devices?: string[]) => api.activateModule(id, enabled, devices), {
    invalidate: [keys.modules],
  })
}

export function useRemoveModule() {
  return useAction((id: string) => api.removeModule(id), {
    success: "已删除模块",
    invalidate: [keys.modules],
  })
}

export function useClearRecords() {
  return useAction(() => api.clearRecords(), {
    success: "已清空全部记录",
    invalidate: [keys.status, ["records"], ["record"], ["ticker"]],
  })
}
