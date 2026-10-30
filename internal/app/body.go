package app

import (
	"encoding/base64"

	"github.com/gtoxlili/mihomo-capture/internal/module"
)

// inspectBody adds a bounded decoded view of a compressed body for the
// dashboard; replay keeps using the wire bytes.
func inspectBody(body Body, limit int) Body {
	if body.Truncated || body.ContentEncoding == "" || limit <= 0 {
		return body
	}
	b := []byte(body.Data)
	if body.Encoding == "base64" {
		var err error
		if b, err = base64.StdEncoding.DecodeString(body.Data); err != nil {
			return body
		}
	}
	decoded, complete, err := module.Decode(b, body.ContentEncoding, limit)
	if err != nil {
		return body
	}
	buf := captureBuffer{limit: limit}
	buf.add(decoded)
	out := buf.body("")
	out.Truncated = !complete
	body.Decoded = &out
	return body
}
