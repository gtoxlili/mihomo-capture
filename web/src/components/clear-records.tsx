import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { useClearRecords, useStatus } from "@/lib/queries"

export function ClearRecordsDialog({
  open,
  onOpenChange,
  onCleared,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCleared?: () => void
}) {
  const clear = useClearRecords()
  const count = useStatus().data?.storage.records ?? 0
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>清空全部 {count} 条记录？</AlertDialogTitle>
          <AlertDialogDescription>
            删除所有设备、所有抓包会话的记录，无法恢复。正在进行的抓包不停，但只记录清空之后新建立的连接，已有连接上的请求不再写进来。
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>取消</AlertDialogCancel>
          <AlertDialogAction variant="danger" onClick={() => clear.mutate([], { onSuccess: () => onCleared?.() })}>
            清空记录
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
