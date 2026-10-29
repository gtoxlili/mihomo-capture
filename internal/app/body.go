package app

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"encoding/base64"
	"io"
	"strings"

	"github.com/andybalholm/brotli"
)

func inspectBody(body Body, limit int) Body {
	if body.Truncated || body.ContentEncoding == "" || limit <= 0 {
		return body
	}
	b := []byte(body.Data)
	if body.Encoding == "base64" {
		var err error
		b, err = base64.StdEncoding.DecodeString(body.Data)
		if err != nil {
			return body
		}
	}
	encodings := strings.Split(strings.ToLower(body.ContentEncoding), ",")
	for i := len(encodings) - 1; i >= 0; i-- {
		var r io.Reader
		var closeFn func() error
		switch strings.TrimSpace(encodings[i]) {
		case "gzip":
			v, err := gzip.NewReader(bytes.NewReader(b))
			if err != nil {
				return body
			}
			r = v
			closeFn = v.Close
		case "deflate":
			v, err := zlib.NewReader(bytes.NewReader(b))
			if err != nil {
				v = flate.NewReader(bytes.NewReader(b))
			}
			r = v
			closeFn = v.Close
		case "br":
			r = brotli.NewReader(bytes.NewReader(b))
		case "identity":
			continue
		default:
			return body
		}
		decoded, err := io.ReadAll(io.LimitReader(r, int64(limit+1)))
		if closeFn != nil {
			_ = closeFn()
		}
		if err != nil {
			return body
		}
		if len(decoded) > limit {
			buf := captureBuffer{limit: limit}
			buf.add(decoded)
			out := buf.body("")
			out.Truncated = true
			body.Decoded = &out
			return body
		}
		b = decoded
	}
	buf := captureBuffer{limit: limit}
	buf.add(b)
	out := buf.body("")
	body.Decoded = &out
	return body
}
