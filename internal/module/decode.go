package module

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"io"
	"strings"

	"github.com/andybalholm/brotli"
)

// Decode undoes a Content-Encoding list (gzip, deflate, br, identity) and
// returns at most limit bytes; complete is false when the content is longer.
func Decode(b []byte, encoding string, limit int) (out []byte, complete bool, err error) {
	encodings := strings.Split(strings.ToLower(encoding), ",")
	// Encodings are listed in the order they were applied.
	for i := len(encodings) - 1; i >= 0; i-- {
		var r io.Reader
		var closer io.Closer
		switch strings.TrimSpace(encodings[i]) {
		case "", "identity":
			continue
		case "gzip", "x-gzip":
			z, err := gzip.NewReader(bytes.NewReader(b))
			if err != nil {
				return nil, false, fmt.Errorf("gzip: %w", err)
			}
			r, closer = z, z
		case "deflate":
			// Servers send both zlib-wrapped and raw deflate as "deflate".
			if z, err := zlib.NewReader(bytes.NewReader(b)); err == nil {
				r, closer = z, z
			} else {
				f := flate.NewReader(bytes.NewReader(b))
				r, closer = f, f
			}
		case "br":
			r = brotli.NewReader(bytes.NewReader(b))
		default:
			return nil, false, fmt.Errorf("unsupported content encoding %q", encodings[i])
		}
		decoded, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
		if closer != nil {
			_ = closer.Close()
		}
		if err != nil {
			return nil, false, fmt.Errorf("%s: %w", strings.TrimSpace(encodings[i]), err)
		}
		if len(decoded) > limit {
			return decoded[:limit], false, nil
		}
		b = decoded
	}
	if len(b) > limit {
		return b[:limit], false, nil
	}
	return b, true, nil
}
