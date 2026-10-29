# HTTP API

[简体中文](api.zh-CN.md)

The default address is `http://127.0.0.1:19090`. Every `/v1` request requires `Authorization: Bearer <API token>`. Send JSON request bodies. `-init` creates the token at `api_token_file`. Public `/ca.crt` and `/ca.pem` endpoints serve only the CA certificate without authentication.

Errors use `{"error":{"code":"invalid_request","message":"..."}}`. Missing authentication returns 401, missing resources 404, state conflicts 409, and controller failures usually 503.

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
| `GET /v1/modules` | Modules, merged hostnames, and `execution_enabled:false` |
| `POST /v1/modules` | Register hostnames with `{"source":"https://example.com/a.sgmodule","arguments":{}}`; source can also be an absolute file path on the service host |
| `DELETE /v1/modules/{id}` | Remove a module |

Record filters are `host`, `path`, `method`, `status`, `q`, `device`, `limit`, and `before`. Limit accepts 1–200 and defaults to 50. Pass `next` from a response as the next page's `before`. Summaries omit bodies. Detail bodies include `encoding`, `data`, `size`, and `truncated`, with `decoded` where applicable. Encoding is `utf8` or `base64`; replay uses the original wire bytes.

Only one trusted device can record at a time. A trust confirmation is bound to the current CA fingerprint, and changing the CA requires confirmation again. The API token can read all records, manage devices, and load modules. Keep it private along with the CA key.
