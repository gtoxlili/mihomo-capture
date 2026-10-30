package app

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

type Device struct {
	ID      string   `json:"id"`
	MAC     string   `json:"mac"`
	Label   string   `json:"label"`
	IPs     []string `json:"ips"`
	Trusted bool     `json:"trusted"`
	// Trust is an operator attestation, not remote OS trust-store inspection.
	TrustEvidence string `json:"trust_evidence,omitempty"`
	CAFingerprint string `json:"ca_fingerprint,omitempty"`
	Online        bool   `json:"online"`
	Capturing     bool   `json:"capturing"`
}

type Capture struct {
	DeviceID      string    `json:"device_id"`
	StartedAt     time.Time `json:"started_at"`
	BlockUDPPorts []uint16  `json:"block_udp_ports,omitempty"`
	Generation    uint64    `json:"generation"`
}

type Body struct {
	Encoding        string `json:"encoding"` // utf8 or base64; always the observed wire bytes.
	Data            string `json:"data"`
	Size            int64  `json:"size"` // Total bytes observed, including bytes omitted by the limit.
	Truncated       bool   `json:"truncated"`
	ContentEncoding string `json:"content_encoding,omitempty"`
	Decoded         *Body  `json:"decoded,omitempty"` // Bounded decompression for inspection; replay uses the wire body.
}

type Message struct {
	Headers  http.Header `json:"headers"`
	Trailers http.Header `json:"trailers,omitempty"`
	Body     Body        `json:"body"`
}

type Frame struct {
	At         time.Time `json:"at"`
	Direction  string    `json:"direction"`
	Opcode     byte      `json:"opcode"`
	Final      bool      `json:"final"`
	Compressed bool      `json:"compressed"`
	Body       Body      `json:"body"` // Payload unmasked; compressed frames remain compressed.
}

type Record struct {
	ID              string    `json:"id"`
	DeviceID        string    `json:"device_id"`
	Session         uint64    `json:"session"`
	At              time.Time `json:"at"`
	Method          string    `json:"method"`
	URL             string    `json:"url"`
	Host            string    `json:"host"`
	Path            string    `json:"path"`
	Status          int       `json:"status"`
	DurationMS      int64     `json:"duration_ms"`
	Request         Message   `json:"request"`
	Response        Message   `json:"response"`
	Frames          []Frame   `json:"frames,omitempty"`
	FramesTruncated bool      `json:"frames_truncated,omitempty"`
	// Curl is empty when the captured request body is incomplete.
	Curl       string `json:"curl,omitempty"`
	Replayable bool   `json:"replayable"`
	Error      string `json:"error,omitempty"`
	Complete   bool   `json:"complete"`
	// Modules names the modules whose rules took effect. The record shows
	// the device's side: the request it sent and the response it received.
	Modules []string `json:"modules,omitempty"`
}

// RecordSummary omits bodies, frames and replay commands. Fetch Record by ID
// for those fields; omitted content is never represented as an empty body.
type RecordSummary struct {
	ID         string    `json:"id"`
	DeviceID   string    `json:"device_id"`
	Session    uint64    `json:"session"`
	At         time.Time `json:"at"`
	Method     string    `json:"method"`
	URL        string    `json:"url"`
	Host       string    `json:"host"`
	Path       string    `json:"path"`
	Status     int       `json:"status"`
	DurationMS int64     `json:"duration_ms"`
	Complete   bool      `json:"complete"`
	Replayable bool      `json:"replayable"`
	Error      string    `json:"error,omitempty"`
	// ContentType is the response media type without parameters.
	ContentType  string   `json:"content_type,omitempty"`
	ResponseSize int64    `json:"response_size"`
	Frames       int      `json:"frames,omitempty"`
	Modules      []string `json:"modules,omitempty"`
}

func (r *Record) summary() RecordSummary {
	media, _, _ := strings.Cut(r.Response.Headers.Get("Content-Type"), ";")
	return RecordSummary{ID: r.ID, DeviceID: r.DeviceID, Session: r.Session, At: r.At, Method: r.Method, URL: r.URL, Host: r.Host, Path: r.Path, Status: r.Status, DurationMS: r.DurationMS, Complete: r.Complete, Replayable: r.Replayable, Error: r.Error, ContentType: strings.ToLower(strings.TrimSpace(media)), ResponseSize: r.Response.Body.Size, Frames: len(r.Frames), Modules: r.Modules}
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
