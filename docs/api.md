# HTTP API

默认地址为 `http://127.0.0.1:19090`。`/v1` 下所有请求都需要 `Authorization: Bearer <API token>`，body 使用 JSON。令牌由 `-init` 生成，位置由 `api_token_file` 配置。`/ca.crt` 和 `/ca.pem` 不需要认证，只返回公开证书。

错误格式为 `{"error":{"code":"invalid_request","message":"..."}}`。无认证返回 401，无资源返回 404，状态冲突返回 409，controller 不可用通常返回 503。

| 方法与路径 | 用途与参数 |
| --- | --- |
| `GET /v1/status` | 服务状态、CA 指纹、当前抓包、mihomo 版本和同步错误 |
| `GET /v1/devices` | 设备列表。本机 ID 为 `local`，网关模式 ID 为 MAC |
| `POST /v1/devices/refresh` | 立即刷新地址和选择 |
| `PUT /v1/devices/{id}` | 改名称，body 为 `{"label":"我的电脑"}` |
| `PUT /v1/devices/{id}/trust` | 确认或撤销信任，body 为 `{"trusted":true,"trust_evidence":"已安装并完全信任当前 CA"}` |
| `PUT /v1/capture` | 开始，body 为 `{"device_id":"local","block_udp_ports":[]}`。阻断 QUIC 可用 `[443]` |
| `DELETE /v1/capture` | 停止录制 |
| `GET /v1/records` | 摘要列表，返回 `items` 与 `next` |
| `GET /v1/records/{id}` | 完整请求、响应、帧与可用的 curl |
| `DELETE /v1/records` | 清空所有记录，并使旧连接不能继续写入 |
| `GET /v1/modules` | 模块、合并后的 hostname 和 `execution_enabled:false` |
| `POST /v1/modules` | 注册 hostname，body 为 `{"source":"https://example.com/a.sgmodule","arguments":{}}`；source 也可为服务电脑上的绝对文件路径 |
| `DELETE /v1/modules/{id}` | 移除模块 |

记录查询支持 `host`、`path`、`method`、`status`、`q`、`device`、`limit` 和 `before`。limit 为 1–200，默认 50；用响应的 next 作为下一页 before。列表不带 body，详情中的 body 包含 encoding、data、size、truncated，必要时带 decoded。encoding 为 utf8 或 base64；重放使用原始字节。

同一时刻只能录制一台受信设备。信任确认绑定当前 CA 的指纹，更换 CA 后需要重新确认。API token 可以读取所有记录、管理设备和加载模块，应与 CA 私钥一起妥善保管。
