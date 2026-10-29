import { Link } from "react-router"

import { CertificateDownload, Fingerprint, InstallSteps } from "@/components/ca-guide"
import { Button } from "@/components/ui/button"
import { formatDateTime } from "@/lib/format"
import { useStatus } from "@/lib/queries"

export function CertificatePage() {
  const ca = useStatus().data?.ca
  if (!ca) return null
  return (
    <div className="mx-auto max-w-4xl space-y-8 px-4 py-6">
      <div>
        <h1 className="text-xl font-semibold tracking-tight">{ca.name}</h1>
        <p className="max-w-prose text-[13px] leading-relaxed text-muted-foreground">
          设备装上这张根证书并完全信任，它的 HTTPS 才能被解密。这张证书由运行服务的电脑生成，有效期到{" "}
          {formatDateTime(ca.not_after)}
          ；更换根证书后，所有设备都要重新安装并重新确认。
        </p>
      </div>
      <section className="space-y-3">
        <h2 className="text-[15px] font-semibold">下载</h2>
        <CertificateDownload ca={ca} />
      </section>
      <section className="space-y-3">
        <h2 className="text-[15px] font-semibold">安装并完全信任</h2>
        <InstallSteps />
      </section>
      <section className="space-y-3">
        <h2 className="text-[15px] font-semibold">SHA-256 指纹</h2>
        <p className="text-[13px] text-muted-foreground">各系统的证书详情里都能看到它，一致说明装的是当前这张。</p>
        <Fingerprint value={ca.fingerprint} />
      </section>
      <div className="flex flex-wrap items-center gap-3 border-t pt-6 text-[13px]">
        <p className="text-muted-foreground">装好并完全信任之后，到设备页把那台设备确认为受信。</p>
        <Button asChild variant="outline" size="sm">
          <Link to="/devices">去设备页</Link>
        </Button>
      </div>
    </div>
  )
}
