package module

import (
	"bytes"
	"strings"
	"unicode/utf8"

	"github.com/dop251/goja"
	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/transform"
)

// textCodec defines TextEncoder and TextDecoder as the WHATWG Encoding
// Standard does. The classes are JavaScript, so prototypes, getters and
// instanceof behave as in browsers; the conversions run in Go.
var textCodec = goja.MustCompile("encoding", `(function (native) {
	class TextEncoder {
		get encoding() { return "utf-8" }
		encode(input = "") { return native.encode(String(input)) }
		encodeInto(source, destination) {
			if (!(destination instanceof Uint8Array)) throw new TypeError("TextEncoder.encodeInto: destination must be a Uint8Array")
			return native.encodeInto(String(source), destination)
		}
	}
	class TextDecoder {
		#decoder
		constructor(label = "utf-8", options = {}) {
			this.#decoder = native.decoder(String(label), Boolean(options?.fatal), Boolean(options?.ignoreBOM))
		}
		get encoding() { return this.#decoder.encoding }
		get fatal() { return this.#decoder.fatal }
		get ignoreBOM() { return this.#decoder.ignoreBOM }
		decode(input, options = {}) { return this.#decoder.decode(input, Boolean(options?.stream)) }
	}
	for (const c of [TextEncoder, TextDecoder]) {
		Object.defineProperty(c.prototype, Symbol.toStringTag, { value: c.name, configurable: true })
	}
	return { TextEncoder, TextDecoder }
})`, true)

func (r *run) installTextCodec() error {
	define, err := r.vm.RunProgram(textCodec)
	if err != nil {
		return err
	}
	fn, _ := goja.AssertFunction(define)
	native := r.vm.NewObject()
	_ = native.Set("encode", func(c goja.FunctionCall) goja.Value {
		// Go strings from JavaScript carry lone surrogates as U+FFFD, as the
		// standard's USVString conversion requires.
		return r.uint8Array([]byte(c.Argument(0).String()))
	})
	_ = native.Set("encodeInto", r.encodeInto)
	_ = native.Set("decoder", r.newTextDecoder)
	classes, err := fn(goja.Undefined(), native)
	if err != nil {
		return err
	}
	o := classes.ToObject(r.vm)
	for _, name := range []string{"TextEncoder", "TextDecoder"} {
		if err := r.vm.Set(name, o.Get(name)); err != nil {
			return err
		}
	}
	return nil
}

// encodeInto writes whole characters only and reports UTF-16 code units read.
func (r *run) encodeInto(c goja.FunctionCall) goja.Value {
	dst, ok := bufferBytes(c.Argument(1))
	if !ok {
		panic(r.vm.NewTypeError("TextEncoder.encodeInto: destination must be a Uint8Array"))
	}
	read, written := 0, 0
	for _, ch := range c.Argument(0).String() {
		size := utf8.RuneLen(ch)
		if written+size > len(dst) {
			break
		}
		utf8.EncodeRune(dst[written:], ch)
		written += size
		read++
		if ch >= 0x10000 {
			read++ // a surrogate pair
		}
	}
	return r.vm.ToValue(map[string]any{"read": read, "written": written})
}

type textDecoder struct {
	name      string
	fatal     bool
	ignoreBOM bool
	t         transform.Transformer
	pending   []byte // an incomplete sequence held for the next streamed call
	started   bool   // output has begun since the last flush
}

func (r *run) newTextDecoder(c goja.FunctionCall) goja.Value {
	label := strings.ToLower(strings.TrimSpace(c.Argument(0).String()))
	enc, err := htmlindex.Get(label)
	name, _ := htmlindex.Name(enc)
	if err != nil || name == "replacement" {
		panic(rangeError(r.vm, "TextDecoder: unsupported encoding label \""+label+"\""))
	}
	d := &textDecoder{name: name, fatal: c.Argument(1).ToBoolean(), ignoreBOM: c.Argument(2).ToBoolean(), t: enc.NewDecoder()}
	o := r.vm.NewObject()
	_ = o.Set("encoding", d.name)
	_ = o.Set("fatal", d.fatal)
	_ = o.Set("ignoreBOM", d.ignoreBOM)
	_ = o.Set("decode", func(c goja.FunctionCall) goja.Value {
		var input []byte
		if v := c.Argument(0); !goja.IsUndefined(v) && !goja.IsNull(v) {
			b, ok := bufferBytes(v)
			if !ok {
				panic(r.vm.NewTypeError("TextDecoder.decode: input must be an ArrayBuffer or a view of one"))
			}
			input = b
		}
		s, ok := d.decode(input, c.Argument(1).ToBoolean())
		if !ok {
			panic(r.vm.NewTypeError("TextDecoder.decode: the data is not valid " + d.name))
		}
		return r.vm.ToValue(s)
	})
	return o
}

// rangeError builds a JavaScript RangeError, which goja offers no helper for.
func rangeError(vm *goja.Runtime, msg string) *goja.Object {
	ctor, _ := goja.AssertConstructor(vm.Get("RangeError"))
	e, _ := ctor(nil, vm.ToValue(msg))
	return e
}

// decode returns false when fatal is set and the data is invalid.
func (d *textDecoder) decode(input []byte, stream bool) (string, bool) {
	src := append(d.pending, input...)
	d.pending = nil
	out := make([]byte, 0, len(src)+len(src)/2+4)
	buf := make([]byte, 4*len(src)+16)
	consumed := 0
	for {
		nDst, nSrc, err := d.t.Transform(buf, src[consumed:], !stream)
		out = append(out, buf[:nDst]...)
		consumed += nSrc
		if err == transform.ErrShortDst {
			continue
		}
		if err == transform.ErrShortSrc {
			d.pending = bytes.Clone(src[consumed:])
		}
		break
	}
	valid := !d.fatal || d.valid(src[:consumed], out)
	s := string(out)
	if !d.ignoreBOM && !d.started && (d.name == "utf-8" || d.name == "utf-16le" || d.name == "utf-16be") {
		s = strings.TrimPrefix(s, "\ufeff")
	}
	d.started = d.started || len(out) > 0
	if !stream || !valid {
		d.t.Reset()
		d.pending, d.started = nil, false
	}
	return s, valid
}

// valid checks the bytes the decoder consumed. The legacy decoders mark
// invalid input with U+FFFD, which none of them can otherwise produce except
// gb18030's own four-byte form of it.
func (d *textDecoder) valid(consumed, out []byte) bool {
	switch d.name {
	case "utf-8":
		return utf8.Valid(consumed)
	case "utf-16le", "utf-16be":
		return validUTF16(consumed, d.name == "utf-16be")
	}
	return !bytes.ContainsRune(out, utf8.RuneError)
}

func validUTF16(b []byte, bigEndian bool) bool {
	if len(b)%2 != 0 {
		return false
	}
	high := false
	for i := 0; i < len(b); i += 2 {
		u := uint16(b[i]) | uint16(b[i+1])<<8
		if bigEndian {
			u = uint16(b[i])<<8 | uint16(b[i+1])
		}
		switch {
		case u >= 0xD800 && u <= 0xDBFF:
			if high {
				return false
			}
			high = true
		case u >= 0xDC00 && u <= 0xDFFF:
			if !high {
				return false
			}
			high = false
		default:
			if high {
				return false
			}
		}
	}
	return !high
}

// bufferBytes reads an ArrayBuffer, a typed array or a DataView, sharing
// its memory.
func bufferBytes(v goja.Value) ([]byte, bool) {
	o, ok := v.(*goja.Object)
	if !ok {
		return nil, false
	}
	if buf, ok := o.Export().(goja.ArrayBuffer); ok {
		return buf.Bytes(), true
	}
	if buf, ok := o.Get("buffer").Export().(goja.ArrayBuffer); ok {
		off, n := o.Get("byteOffset").ToInteger(), o.Get("byteLength").ToInteger()
		if b := buf.Bytes(); off >= 0 && n >= 0 && off+n <= int64(len(b)) {
			return b[off : off+n], true
		}
	}
	return nil, false
}
