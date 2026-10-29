package app

import (
	"bytes"
	"io"
	"sync"
	"time"

	"github.com/gobwas/ws"
)

// frameObserver observes wire bytes without buffering a whole frame or changing
// fragmentation, control frames, masks, compression, or forwarding behavior.
type frameObserver struct {
	header    []byte
	h         *ws.Header
	remaining int64
	offset    int
	body      captureBuffer
	direction string
	emit      func(Frame)
	disabled  bool
}

func (o *frameObserver) feed(p []byte) {
	for len(p) > 0 && !o.disabled {
		if o.h == nil {
			o.header = append(o.header, p[0])
			p = p[1:]
			if len(o.header) < 2 {
				continue
			}
			n := 2
			switch o.header[1] & 127 {
			case 126:
				n += 2
			case 127:
				n += 8
			}
			if o.header[1]&128 != 0 {
				n += 4
			}
			if len(o.header) < n {
				continue
			}
			h, err := ws.ReadHeader(bytes.NewReader(o.header))
			o.header = nil
			if err != nil || h.Length < 0 {
				o.disabled = true
				return
			}
			o.h = &h
			o.remaining = h.Length
			o.offset = 0
			o.body.b = nil
			o.body.size = 0
		}
		if o.remaining > 0 {
			n := int64(len(p))
			if n > o.remaining {
				n = o.remaining
			}
			// Keep only the payload prefix. Cipher offset counts the entire frame.
			keep := min(int(n), o.body.limit-len(o.body.b))
			if keep > 0 {
				b := bytes.Clone(p[:keep])
				if o.h.Masked {
					ws.Cipher(b, o.h.Mask, o.offset)
				}
				o.body.b = append(o.body.b, b...)
			}
			o.body.size += n
			o.offset += int(n)
			o.remaining -= n
			p = p[n:]
		}
		if o.remaining == 0 {
			o.emit(Frame{At: time.Now().UTC(), Direction: o.direction, Opcode: byte(o.h.OpCode), Final: o.h.Fin, Compressed: o.h.Rsv != 0, Body: o.body.body("")})
			o.h = nil
		}
	}
}

type websocketTap struct {
	io.ReadWriteCloser
	read, write frameObserver
	finish      func()
	once        sync.Once
}

func newWebsocketTap(rw io.ReadWriteCloser, a *App, f *flowState, finish func()) *websocketTap {
	frameLimit := a.Config.MaxFrameBytes
	if f.release == nil {
		frameLimit = 0
	}
	emit := func(frame Frame) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.r.Frames) >= a.Config.MaxFrames {
			f.r.FramesTruncated = true
			return
		}
		f.r.Frames = append(f.r.Frames, frame)
		a.record(f.t, f.r)
	}
	w := &websocketTap{ReadWriteCloser: rw, finish: finish}
	w.read = frameObserver{direction: "server_to_client", body: captureBuffer{limit: frameLimit}, emit: emit}
	w.write = frameObserver{direction: "client_to_server", body: captureBuffer{limit: frameLimit}, emit: emit}
	return w
}
func (w *websocketTap) Read(p []byte) (int, error) {
	n, err := w.ReadWriteCloser.Read(p)
	w.read.feed(p[:n])
	if err != nil {
		w.once.Do(w.finish)
	}
	return n, err
}
func (w *websocketTap) Write(p []byte) (int, error) {
	n, err := w.ReadWriteCloser.Write(p)
	w.write.feed(p[:n])
	if err != nil {
		w.once.Do(w.finish)
	}
	return n, err
}
func (w *websocketTap) Close() error {
	err := w.ReadWriteCloser.Close()
	w.once.Do(w.finish)
	return err
}
