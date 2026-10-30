# HTTP API

[简体中文](api.zh-CN.md)

The default address is `http://127.0.0.1:19090`. Every `/v1` request requires `Authorization: Bearer <API token>`. Send JSON request bodies. `-init` creates the token at `api_token_file`. Public `/ca.crt` and `/ca.pem` endpoints serve only the CA certificate without authentication.

Errors use `{"error":{"code":"invalid_request","message":"..."}}`. Invalid input returns 400, missing authentication 401, missing resources 404, state conflicts 409, and controller failures usually 503.

| Method and path | Behavior and input |
| --- | --- |
| `GET /v1/status` | Service status, CA fingerprint, active capture, mihomo version, and synchronization errors |
| `GET /v1/devices` | Device list; the local device ID is `local`, and gateway IDs are MAC addresses |
| `POST /v1/devices/refresh` | Refresh source addresses and selection immediately |
| `PUT /v1/devices/{id}` | Rename with `{"label":"My computer"}` |
| `PUT /v1/devices/{id}/trust` | Confirm or revoke trust with `{"trusted":true,"trust_evidence":"Installed and fully trusted the current CA"}` |
| `PUT /v1/capture` | Start with `{"device_id":"local","block_udp_ports":[]}`; use `[443]` to block UDP 443 for TCP fallback |
| `DELETE /v1/capture` | Stop recording |
| `GET /v1/records` | Record summaries, returning `items` and `next` |
| `GET /v1/records/{id}` | Complete request, response, frames, and curl command when available |
| `DELETE /v1/records` | Clear all records and invalidate old connections' write tickets |
| `GET /v1/modules` | Module summaries: name, `enabled`, `devices`, hostnames and rule counts, in the order rules apply |
| `POST /v1/modules` | Create a module from a [definition](#modules); it starts disabled with no devices. Returns 201 and the module |
| `GET /v1/modules/{id}` | The complete module: its definition plus `enabled`, `devices`, `created_at` and `updated_at` |
| `PUT /v1/modules/{id}` | Replace the definition; activation is kept |
| `PUT /v1/modules/{id}/activation` | `{"enabled":true,"devices":["local"]}`; omitting `devices` keeps the list |
| `GET /v1/modules/{id}/logs` | The 200 latest log entries, newest first: script console output and notifications, skipped rules, failed scripts |
| `DELETE /v1/modules/{id}` | Delete a module, its log and its scripts' keyless store entries |

Record filters are `host`, `path`, `method`, `status`, `q`, `device`, `limit`, and `before`. Limit accepts 1–200 and defaults to 50. Pass `next` from a response as the next page's `before`. Summaries omit bodies. Detail bodies include `encoding`, `data`, `size`, and `truncated`, with `decoded` where applicable. Encoding is `utf8` or `base64`; replay uses the original wire bytes. A record shows the device's side, the request it sent and the response it received; `modules` names the modules whose rules took effect.

Only one trusted device can record at a time. A trust confirmation is bound to the current CA fingerprint, and changing the CA requires confirmation again. The API token can read all records, manage devices, and run module scripts on the service host. Keep it private along with the CA key.

## Modules

A module decrypts the hosts it lists for the trusted devices it is enabled on, and processes their requests. Its definition is the body of `POST` and `PUT /v1/modules`; unknown fields are rejected, and the stored definition has every default filled in.

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

| Field | Content |
| --- | --- |
| `hostnames` | Hosts to decrypt: `*` and `?` wildcards, a leading `-` excludes, the first matching entry decides. An entry covers every port of the host; ports are rejected. A module acts only on requests to these hosts, during a capture too |
| `header_rewrites` | `type` `http-request` (default) or `http-response`, URL `pattern`, `action`, header `name`, and `value`, or `regex` and `replacement`. `header-add` appends a line, `header-del` removes every line, `header-replace` sets the value of a header that is present, and `header-replace-regex` rewrites each present line. `Content-Length` and `Transfer-Encoding` are refused |
| `url_rewrites` | `pattern`, `replacement`, `type`. The replacement substitutes the pattern's matches in the URL. `header` (default) rewrites the request in place and sets Host, unseen by the client; a result that is not an http, https, ws or wss URL is discarded. `302` and `307` answer with the result as `Location`. `reject` answers 404 with an empty body |
| `map_locals` | Answers without contacting the upstream. `pattern`, `data_type` `text` (default, Content-Type `text/plain`), `base64` (`application/octet-stream`) or `tiny-gif` (a 1×1 GIF, `image/gif`), `data`, `status_code` 200–999 (default 200), `headers` object, which may override the Content-Type |
| `body_rewrites` | `type` `http-request`, `http-response`, `http-request-jq` or `http-response-jq`, URL `pattern`. The regex types take `replacements` (`[{"regex","replacement"}]`), applied in order to UTF-8 bodies, with `^` and `$` matching at each line; `^$` can fill an empty body. The jq types take `jq`, applied to JSON bodies; its first output replaces the body with object keys sorted, and no output keeps it |
| `scripts` | `name` (unique in the module), `type` `http-request` or `http-response`, `pattern`, `code`, `requires_body`, `binary_body_mode`, `full_header_mode`, `max_size` (bytes, default 10 MiB, up to 32 MiB, `-1` for 32 MiB), `timeout` (seconds, default 5, up to 60), `argument` |

A request goes through header rewrite, URL rewrite, map local, body rewrite and then the first matching `http-request` script; a response through header rewrite, body rewrite and the first matching `http-response` script. Header and body rewrites apply every matching rule, URL rewrite and map local only the first, and a message runs one script. A device's modules apply in the module list's order. Responses produced by the request phase skip the response phase.

Patterns are regular expressions matched against the full URL, such as `https://api.example.com/v1/feed?x=1`. RE2 syntax runs in linear time; patterns that need lookaround or backreferences use a backtracking engine with a 500 ms match limit. In replacement templates `$1` names a group, taking no more digits than the number of groups has, `${name}` names a named group, and `\` escapes the next character.

Body rewrites and scripts see bodies after gzip, deflate or br decoding; a changed body is sent uncompressed with a new length. Bodies larger than 10 MiB pass through unmodified by body rewrites. Event streams, WebSocket upgrades and responses to HEAD, 204 and 304 carry no body to rewrite.

### Scripts

Scripts are JavaScript run by goja, with ECMAScript 2020 and later syntax and no browser or Node.js APIs. A script ends by calling `$done`; later calls are ignored. Throwing, running past `timeout`, or ending without `$done` once nothing is pending leaves the message unchanged and adds a log entry. At most four scripts, or one per CPU when more, run at once; later ones wait within their own timeout.

| Global | Content |
| --- | --- |
| `$request` | `url`, `method`, `headers` and `id`, shared by the request and response scripts of one exchange. An `http-request` script with `requires_body` also gets `body` when the body is not empty: a string, or a `Uint8Array` with `binary_body_mode` |
| `$response` | In `http-response` scripts: `status`, `headers`, and `body` under the same conditions |
| `$done(result)` | `http-request`: `url` replaces the URL and keeps Host; `headers` replaces every header, with `Host` setting the host; `body` replaces the body when `requires_body` is set; `response` (`status` 200–999, default 200, `headers`, `body`) answers without the upstream; `abort: true` closes the connection. `http-response`: `status`, `headers`, `body` and `abort` likewise. `$done()` and `$done({})` change nothing. Framing headers in a result are ignored and recomputed |
| `$argument`, `$script`, `$environment` | The `argument` string when set; `name`, `type`, `startTime` (seconds), `sessionID` and `binaryBodyMode`; `system` and `language` |
| `$persistentStore` | `read([key])` returns a string or `null`; `write(value, [key])` returns whether it was stored, and `null` deletes. Without a key the entry belongs to the script; a key is shared by every script. Values up to 4 MiB, 32 MiB in all, kept across restarts |
| `$httpClient` | `get`, `post`, `put`, `delete`, `head`, `options` and `patch` take a URL or `{url, headers, body, timeout, policy, insecure, "auto-redirect", "binary-mode", "full-header-mode"}` and a callback `(error, {status, headers}, data)`. A plain object body is sent as JSON. Requests leave from the service host; `policy` names a mihomo policy the rules use and leaves through it. `timeout` defaults to 5 seconds, redirects are followed unless `auto-redirect` is false, at most 20 requests run at once per script, and response bodies are capped at 32 MiB |
| `$notification.post(title, subtitle, body)` | Adds a log entry |
| `$utils.ungzip(bytes)` | A `Uint8Array`, or `null` when the input is not gzip |
| `console` | `log`, `info`, `debug`, `warn` and `error` add log entries; objects print as JSON |
| `setTimeout`, `clearTimeout` | Up to 64 pending timers, cancelled when the script ends |

Header objects answer to any letter case. Repeated header lines appear joined with `, `, and a header returned with the value it was given keeps its original lines, so an untouched `Set-Cookie` survives. With `full_header_mode`, headers are an array of `{field, value}` objects instead, one per line. Request headers include `Host`.

A body larger than `max_size` is not given to the script, which still runs. A script that returns `body` without `requires_body` has it ignored.

### Activation and limits

Enabling, disabling, deleting and editing apply to the next request, also on open connections. A change to `devices` applies to connections opened afterwards; an open connection keeps the module set it arrived with. Listed devices that are not trusted are ignored until they are. The module log keeps 200 entries in memory: console output, notifications, skipped rules, and failed scripts.

A service holds up to 64 modules, each with up to 256 hostnames, 1024 rules per list and 64 scripts of up to 1 MiB; a definition body may be 4 MiB.
