# Mihomo Capture

通过电脑上已有的 mihomo 查看 HTTP、HTTPS 请求和 WebSocket 帧。默认抓本机；电脑用作 mihomo 网关时，也可以选择一台局域网设备。

抓包服务沿用 mihomo 的分流策略。请求先按原始来源地址、端口和规则选出口，再进入抓包服务，所以抓包期间仍然走原来的 DIRECT、代理节点或策略组。服务附带一个网页，用来管理证书、开关抓包、查看请求和复制 curl。

目前支持 macOS 和 Linux。需要同机运行的原版 mihomo，已用 v1.19.33 验证。Windows 尚未验证。

## 从源码运行

需要 Go 1.27.3+、Node.js 22.18+ 和 pnpm 12。先构建网页，再构建二进制：

```sh
pnpm --dir web install --frozen-lockfile
pnpm --dir web build
go build -o bin/mihomo-capture ./cmd/mihomo-capture
cp config.example.yaml config.yaml
```

编辑 `config.yaml`，填入这几个值：

- `controller_url` 或 `controller_socket`，二选一。HTTP controller 示例为 `http://127.0.0.1:9090`。
- `controller_token_file`，如果 mihomo 设置了 `secret`，把同一个值放进这个权限为 `0600` 的文件。没有 secret 可以省略。
- `gateway_config`，mihomo 实际加载的配置文件。使用桌面客户端时通常是客户端生成的最终配置，不是订阅文件。
- `gateway_state_dir`，mihomo home 目录下一个可写的新目录，用来保存两份抓包选择规则。

所有相对路径以配置文件所在目录为准。服务与 mihomo 应使用同一用户运行。controller 不要经过系统代理，也不要暴露到公网。

```sh
bin/mihomo-capture -config config.yaml -init
bin/mihomo-capture -config config.yaml -check
scripts/run.sh config.yaml
```

打开 <http://127.0.0.1:19090>，用 `cat data/api-token` 读取登录令牌。下载网页提供的 `Mihomo Capture CA`，在当前电脑安装并信任。回到设备页，确认“本机”已经信任证书，再开始抓包。服务不会替你修改系统信任库或系统代理。

应用的流量必须已经经过 mihomo，例如系统 HTTP 代理或 TUN。直接连接网络、绕过 mihomo 的请求不会出现。`local` 模式自动使用回环和活动网卡地址；如果 mihomo 显示的来源是额外的 TUN 地址，填入 `local_source_ips`，然后重启服务。

## 抓局域网设备

把 `mode` 改成 `gateway`，用 `lan` 指定 IPv4 子网，例如 `192.168.1.0/24`。目标设备必须通过这台电脑的 mihomo 出网。macOS 使用 ARP 邻居表，Linux 需要 `iproute2`。一次只能抓一台设备，设备身份按 MAC 保存。

手机不能访问电脑的回环地址。可以把 `ca_listen` 设为电脑的 LAN 地址和一个空闲端口，手机扫码下载证书。这个监听器只提供公开证书；Dashboard 和 API 仍可以保留在 `127.0.0.1`。安装证书后，在 Dashboard 确认该设备已信任，再开始抓包。确认只是人工记录，服务无法检查远端信任库。

## 能抓到什么

请求和响应保留头、body、状态码与耗时；WebSocket 保留方向、opcode、分片和压缩标记。二进制 body 用 base64 表示，gzip、deflate、Brotli 内容另有解压视图。完整请求可以复制成 curl，截断的请求不提供重放命令。列表支持 host、路径、方法、状态码和关键词筛选。

默认保存 24 小时、最多 5000 条、总预算 256 MiB。每个 body 最多 1 MiB；WebSocket 每条请求最多 128 帧，每帧最多 16 KiB。超限会截断记录，网络传输继续。停止、换设备和清空记录都会让旧连接失去继续写记录的资格。进程重启后不会自动恢复抓包。

证书固定的应用可能拒绝连接。HTTP/3 走 UDP，不在 HTTP 解密范围内；开始抓包时可选阻断该设备 UDP 443，让支持回落的应用改走 TCP，同端口上的其他 UDP 也会受到影响。非 HTTP TCP 和无法解密的协议会透传。

模块页保留了一个可选功能：读取 Surge `.sgmodule` 的 MITM hostname。它会让受信设备在停止录制后仍对这些 hostname 解密，但不保存记录。模块脚本、改写和规则不执行。不需要这个行为时不要注册模块。

## 停止与移除

推荐通过 `scripts/run.sh` 启动。它在子进程退出后清空抓包选择，包括子进程崩溃的情况。正常停止请按 Ctrl-C。强制杀死包装脚本和服务、机器断电或 controller 失联时，清理无法保证即时完成；可运行：

```sh
bin/mihomo-capture -config config.yaml -clear-gateway
# 停止服务后，重载 mihomo 自己的配置，移除附加节点与监听器：
bin/mihomo-capture -config config.yaml -remove-gateway
```

服务不修改 mihomo 原始配置。mihomo 自己重启或重载后也会丢弃这些附加项。卸载证书请在系统信任库中删除 `Mihomo Capture CA`，然后删除本项目生成的 data 目录。这里保存了 CA 私钥和可能包含 cookie、密码的抓包内容，不要提交或分享它。

## 开发

```sh
go test -race ./...
go vet ./...
pnpm --dir web lint
pnpm --dir web build
# 原版 core 集成测试启动临时实例，不接触正在使用的 mihomo：
MIHOMO_TEST_BINARY=/absolute/path/to/mihomo go test -race ./internal/gateway
```

`pnpm --dir web dev` 将 API 请求转发到 `127.0.0.1:19090`，可用 `MIHOMO_CAPTURE_API` 改地址。[接入原理与兼容限制](docs/mihomo.md)说明服务如何加载规则；[HTTP API](docs/api.md)可用于编写自己的调用程序。

MIT License。项目不分发 mihomo 二进制，mihomo 保留其自身许可证。
