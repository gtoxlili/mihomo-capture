import { QRCodeSVG } from "qrcode.react"
import { CopyIcon, DownloadIcon } from "lucide-react"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import type { Status } from "@/lib/api"
import { groupHex } from "@/lib/format"
import { copyText } from "@/lib/hooks"

type CA = Status["ca"]

// LAN clients can download the CA from the optional certificate listener.
export function CertificateDownload({ ca, size = 168 }: { ca: CA; size?: number }) {
  return (
    <div className="flex flex-col gap-4 sm:flex-row sm:items-start">
      {ca.lan_url ? (
        <div className="shrink-0 rounded-lg border bg-white p-3">
          <QRCodeSVG value={ca.lan_url} size={size} marginSize={0} fgColor="#17202b" title={ca.lan_url} />
        </div>
      ) : null}
      <div className="min-w-0 space-y-3 text-[13px]">
        {ca.lan_url ? (
          <div className="space-y-1">
            <p>同一局域网里的手机扫码，或在它的浏览器里打开：</p>
            <button
              type="button"
              onClick={() => copyText(ca.lan_url).then(() => toast.success("已复制链接"))}
              className="group flex items-center gap-2 font-mono text-xs text-signal hover:underline"
            >
              {ca.lan_url}
              <CopyIcon className="size-3.5 text-muted-foreground group-hover:text-foreground" />
            </button>
            <p className="text-muted-foreground">这个地址只在当前局域网里能打开，下载不需要令牌。</p>
          </div>
        ) : (
          <p className="text-muted-foreground">服务没有配置局域网证书地址，设备只能从下面的链接下载。</p>
        )}
        <div className="space-y-1.5">
          <p>这台电脑：</p>
          <div className="flex flex-wrap items-center gap-2">
            <Button asChild variant="outline" size="sm">
              <a href="/ca.crt" download>
                <DownloadIcon />
                下载 {ca.name}
              </a>
            </Button>
            <a href="/ca.pem" download className="text-xs text-signal hover:underline">
              PEM 格式
            </a>
          </div>
        </div>
      </div>
    </div>
  )
}

export function Fingerprint({ value }: { value: string }) {
  return (
    <div className="flex items-start gap-2">
      <code className="min-w-0 flex-1 rounded-md border bg-background px-3 py-2 font-mono text-[13px] leading-relaxed tracking-wide break-words select-all">
        {groupHex(value.toUpperCase())}
      </code>
      <Button
        variant="ghost"
        size="icon-sm"
        aria-label="复制指纹"
        onClick={() => copyText(value).then(() => toast.success("已复制指纹"))}
      >
        <CopyIcon />
      </Button>
    </div>
  )
}

const platforms = [
  {
    id: "ios",
    name: "iPhone / iPad",
    steps: [
      "用 Safari 打开证书链接，允许下载描述文件。",
      "在「设置 > 通用 > VPN 与设备管理」里安装它。",
      "在「设置 > 通用 > 关于本机 > 证书信任设置」里打开这张根证书的完全信任。漏了这一步，HTTPS 全部报错。",
    ],
  },
  {
    id: "macos",
    name: "macOS",
    steps: [
      "双击下载的证书，导入「登录」或「系统」钥匙串。",
      "在「钥匙串访问」里打开这张证书，在「信任」里把「使用此证书时」设为「始终信任」。",
    ],
    command:
      "sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain mihomo-capture-ca.pem",
    commandNote: "或者在终端里执行，对这台 Mac 的所有用户生效：",
  },
  {
    id: "windows",
    name: "Windows",
    steps: ["双击证书，选择「安装证书」。", "存储位置选「受信任的根证书颁发机构」。"],
  },
  {
    id: "android",
    name: "Android",
    steps: [
      "在「设置 > 安全 > 加密与凭据 > 安装证书 > CA 证书」里选择下载的证书。",
      "Android 7 起，App 默认只信任系统自带的证书，用户装的证书对它们无效，这些 App 的 HTTPS 在抓包时握手失败。",
    ],
  },
  {
    id: "firefox",
    name: "Firefox",
    steps: [
      "Firefox 默认用自己的证书库：在「设置 > 隐私与安全 > 证书 > 查看证书 > 证书颁发机构」里导入，勾选信任它标识网站。",
      "Safari、Chrome 和 Edge 使用系统的信任设置，不需要另外导入。",
    ],
  },
]

export function InstallSteps() {
  return (
    <Tabs defaultValue="ios" className="gap-3">
      <TabsList className="flex-wrap">
        {platforms.map((p) => (
          <TabsTrigger key={p.id} value={p.id} className="text-[13px]">
            {p.name}
          </TabsTrigger>
        ))}
      </TabsList>
      {platforms.map((p) => (
        <TabsContent key={p.id} value={p.id} className="space-y-3">
          <ol className="list-decimal space-y-1.5 pl-5 text-[13px] leading-relaxed marker:text-muted-foreground">
            {p.steps.map((s) => (
              <li key={s}>{s}</li>
            ))}
          </ol>
          {p.command && (
            <div className="space-y-1">
              <p className="text-[13px] text-muted-foreground">{p.commandNote}</p>
              <code className="block rounded-md border bg-background px-3 py-2 font-mono text-xs break-all select-all">
                {p.command}
              </code>
            </div>
          )}
        </TabsContent>
      ))}
    </Tabs>
  )
}
