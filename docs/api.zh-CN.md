# HTTP API

[English](api.md)

默认地址为 `http://127.0.0.1:19090`。`/v1` 下所有请求都需要 `Authorization: Bearer <API token>`，body 使用 JSON。令牌由 `-init` 生成，位置由 `api_token_file` 配置。`/ca.crt` 和 `/ca.pem` 不需要认证，只返回公开证书。

错误格式为 `{"error":{"code":"invalid_request","message":"..."}}`。输入不合法返回 400，无认证返回 401，无资源返回 404，状态冲突返回 409，controller 不可用通常返回 503。

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
| `GET /v1/modules` | 模块摘要：名称、`enabled`、`devices`、主机名和各类规则条数，顺序即规则生效顺序 |
| `POST /v1/modules` | 用[定义](#模块)新建模块，初始为停用且没有设备。返回 201 和模块 |
| `GET /v1/modules/{id}` | 完整模块：定义加上 `enabled`、`devices`、`created_at`、`updated_at` |
| `PUT /v1/modules/{id}` | 替换定义，启用状态不变 |
| `PUT /v1/modules/{id}/activation` | `{"enabled":true,"devices":["local"]}`；不传 `devices` 时保留原列表 |
| `GET /v1/modules/{id}/logs` | 最近 200 条日志，最新的在前：脚本的 console 输出与通知、被跳过的规则、失败的脚本 |
| `DELETE /v1/modules/{id}` | 删除模块、它的日志和它的脚本保存的无键数据 |

记录查询支持 `host`、`path`、`method`、`status`、`q`、`device`、`limit` 和 `before`。limit 为 1–200，默认 50；用响应的 next 作为下一页 before。列表不带 body，详情中的 body 包含 encoding、data、size、truncated，必要时带 decoded。encoding 为 utf8 或 base64；重放使用原始字节。记录是设备一侧的视角：它发出的请求和它收到的响应；`modules` 列出规则生效了的模块。

同一时刻只能录制一台受信设备。信任确认绑定当前 CA 的指纹，更换 CA 后需要重新确认。API token 可以读取所有记录、管理设备，并在服务所在的电脑上运行模块脚本，应与 CA 私钥一起妥善保管。

## 模块

模块为启用它的受信设备解密它列出的主机，并处理这些主机的请求。定义就是 `POST` 和 `PUT /v1/modules` 的 body，不认识的字段会被拒绝，保存下来的定义会补全所有默认值。

```json
{
  "name": "Example",
  "hostnames": ["-static.example.com", "*.example.com"],
  "header_rewrites": [{"type": "http-request", "pattern": "^https://api\\.example\\.com/", "action": "header-add", "name": "DNT", "value": "1"}],
  "url_rewrites": [{"pattern": "^https://ads\\.example\\.com/", "type": "reject"}],
  "map_locals": [{"pattern": "/config$", "data_type": "text", "data": "{}", "status_code": 200, "headers": {"Content-Type": "application/json"}}],
  "body_rewrites": [{"type": "http-response-jq", "pattern": "/feed$", "jq": ".ads = []"}],
  "scripts": [{"name": "tag", "type": "http-response", "pattern": "/profile$", "requires_body": true, "code": "$done({body: $response.body})"}]
}
```

| 字段 | 内容 |
| --- | --- |
| `hostnames` | 要解密的主机：`*` 与 `?` 通配，前缀 `-` 表示排除，第一个匹配的条目决定。条目覆盖主机的所有端口，带端口的写法会被拒绝。模块只处理这些主机的请求，抓包时也一样 |
| `header_rewrites` | `type` 为 `http-request`（默认）或 `http-response`，URL `pattern`，`action`，头名 `name`，以及 `value`，或者 `regex` 和 `replacement`。`header-add` 追加一行，`header-del` 删除所有同名行，`header-replace` 只给已有的头设新值，`header-replace-regex` 逐行改写已有的值。拒绝改写 `Content-Length` 和 `Transfer-Encoding` |
| `url_rewrites` | `pattern`、`replacement`、`type`。替换结果是把 URL 里匹配 pattern 的部分换成 replacement。`header`（默认）原地改写请求并更新 Host，客户端无感；结果不是 http、https、ws 或 wss URL 时放弃改写。`302`、`307` 以结果为 `Location` 让客户端重定向。`reject` 返回 404 和空正文 |
| `map_locals` | 不访问上游，直接应答。`pattern`，`data_type` 为 `text`（默认，Content-Type 为 `text/plain`）、`base64`（`application/octet-stream`）或 `tiny-gif`（1×1 的 GIF，`image/gif`），`data`，`status_code` 200–999（默认 200），`headers` 对象，可覆盖 Content-Type |
| `body_rewrites` | `type` 为 `http-request`、`http-response`、`http-request-jq` 或 `http-response-jq`，URL `pattern`。正则方式用 `replacements`（`[{"regex","replacement"}]`），依次作用于 UTF-8 正文，`^` 和 `$` 匹配每一行，`^$` 可以给空正文填内容。jq 方式用 `jq`，作用于 JSON 正文：第一个输出替换正文，对象的键按字母排序；没有输出时保留原文 |
| `scripts` | `name`（模块内唯一），`type` 为 `http-request` 或 `http-response`，`pattern`、`code`、`requires_body`、`binary_body_mode`、`full_header_mode`，`max_size`（字节，默认 10 MiB，最多 32 MiB，`-1` 即 32 MiB），`timeout`（秒，默认 5，最多 60），`argument` |

请求依次经过 Header 改写、URL 改写、本地映射、Body 改写，最后是第一个匹配的 `http-request` 脚本；响应依次经过 Header 改写、Body 改写和第一个匹配的 `http-response` 脚本。Header 和 Body 改写逐条生效，URL 改写和本地映射只取第一条，每个消息只运行一个脚本。同一设备上的多个模块按模块列表的顺序生效。请求阶段直接给出的响应不再经过响应阶段。

pattern 是匹配完整 URL（如 `https://api.example.com/v1/feed?x=1`）的正则。RE2 语法按线性时间执行；需要环视或反向引用的写法改用回溯引擎，单次匹配限时 500 ms。替换模板里 `$1` 引用分组，取的数字位数不超过分组数的位数，`${name}` 引用命名分组，`\` 转义下一个字符。

Body 改写和脚本看到的是按 gzip、deflate、br 解压后的正文；正文被改动时以未压缩形式发出，并重算长度。超过 10 MiB 的正文不经 Body 改写原样放行。事件流、WebSocket 升级，以及 HEAD、204、304 的响应没有可改写的正文。

### 脚本

脚本是 JavaScript，由 goja 执行，支持 ECMAScript 2020 及以后的语法，没有浏览器或 Node.js 的 API。脚本调用 `$done` 结束，之后的调用被忽略。抛出异常、超过 `timeout`，或者在没有待办回调时结束而没调用 `$done`，消息原样放行并记一条日志。同时最多运行 4 个脚本，CPU 更多时每个 CPU 一个；其余的在各自的超时内排队。

| 全局对象 | 内容 |
| --- | --- |
| `$request` | `url`、`method`、`headers`，以及同一次交换里请求脚本与响应脚本共用的 `id`。设了 `requires_body` 的 `http-request` 脚本在正文非空时还有 `body`：字符串，`binary_body_mode` 时为 `Uint8Array` |
| `$response` | `http-response` 脚本里有 `status`、`headers`，`body` 的条件同上 |
| `$done(result)` | `http-request`：`url` 替换 URL，Host 不变；`headers` 替换全部请求头，其中 `Host` 设定主机；`body` 在设了 `requires_body` 时替换正文；`response`（`status` 200–999，默认 200，`headers`，`body`）不访问上游直接应答；`abort: true` 断开连接。`http-response`：`status`、`headers`、`body`、`abort` 同理。`$done()` 与 `$done({})` 不做改动。结果里的分帧头被忽略并重算 |
| `$argument`、`$script`、`$environment` | 设了 `argument` 时为该字符串；`name`、`type`、`startTime`（秒）、`sessionID`、`binaryBodyMode`；`system` 与 `language` |
| `$persistentStore` | `read([key])` 返回字符串或 `null`；`write(value, [key])` 返回是否存下，写 `null` 删除。不带 key 时条目属于这个脚本；带 key 时所有脚本共用。单个值最多 4 MiB，总量 32 MiB，重启后保留 |
| `$httpClient` | `get`、`post`、`put`、`delete`、`head`、`options`、`patch` 接受 URL 或 `{url, headers, body, timeout, policy, insecure, "auto-redirect", "binary-mode", "full-header-mode"}`，以及回调 `(error, {status, headers}, data)`。普通对象作 body 时按 JSON 发送。请求从服务所在的电脑发出；`policy` 指定规则里用到的某个 mihomo 策略时经它发出。`timeout` 默认 5 秒，`auto-redirect` 为 false 之外都跟随重定向，每个脚本同时最多 20 个请求，响应正文上限 32 MiB |
| `$notification.post(title, subtitle, body)` | 记一条日志 |
| `$utils.ungzip(bytes)` | 返回 `Uint8Array`，输入不是 gzip 时返回 `null` |
| `console` | `log`、`info`、`debug`、`warn`、`error` 记日志，对象按 JSON 打印 |
| `setTimeout`、`clearTimeout` | 最多 64 个待触发的定时器，脚本结束时全部取消 |

头对象按任意大小写都能取到。同名的多行头以 `, ` 连接成一个值；原样返回的头保留原来的各行，所以没动过的 `Set-Cookie` 不受影响。`full_header_mode` 时头改为 `{field, value}` 对象的数组，每行一个。请求头里包含 `Host`。

正文超过 `max_size` 时脚本照常运行，只是拿不到正文。没设 `requires_body` 的脚本返回的 `body` 被忽略。

### 启用与上限

启用、停用、删除和修改定义对下一个请求生效，已打开的连接也一样。改动 `devices` 只影响之后新建的连接，已打开的连接保留它建立时的模块组合。列表里未受信的设备在受信之前不受影响。模块日志在内存里保留 200 条：console 输出、通知、被跳过的规则和失败的脚本。

一个服务最多 64 个模块，每个模块最多 256 个主机名、每类规则 1024 条、64 个脚本，每个脚本最大 1 MiB；一个定义的 body 最大 4 MiB。
