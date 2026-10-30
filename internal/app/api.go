package app

import (
	"crypto/subtle"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/gtoxlili/mihomo-capture/internal/module"
)

// Routes is the API listener's handler. The certificate and the dashboard's
// files hold no data and are public; everything under /v1 needs the API token.
func (a *App) Routes(ca *x509.Certificate, dashboard http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer, middleware.GetHead)
	certificateRoutes(r, ca.Raw)
	r.Route("/v1", func(r chi.Router) {
		r.Use(middleware.SetHeader("Cache-Control", "no-store"), a.requireToken)
		r.NotFound(func(w http.ResponseWriter, r *http.Request) { apiError(w, 404, errors.New("no such API route")) })
		r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
			apiError(w, 405, errors.New("method not allowed on this route"))
		})
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequestSize(64 << 10))
			r.Get("/status", a.apiStatus(ca))
			r.Get("/devices", a.apiDevices)
			r.Post("/devices/refresh", a.apiRefreshDevices)
			r.Put("/devices/{id}", a.apiRenameDevice)
			r.Put("/devices/{id}/trust", a.apiTrust)
			r.Put("/capture", a.apiStartCapture)
			r.Delete("/capture", a.apiStopCapture)
			r.Get("/modules", a.apiModules)
			r.Get("/modules/{id}", a.apiModule)
			r.Put("/modules/{id}/activation", a.apiActivateModule)
			r.Get("/modules/{id}/logs", a.apiModuleLogs)
			r.Delete("/modules/{id}", a.apiRemoveModule)
			r.Get("/records", a.apiRecords)
			r.Get("/records/{id}", a.apiRecord)
			r.Delete("/records", a.apiClearRecords)
		})
		// Definitions carry their scripts' code.
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequestSize(maxDefinitionBytes))
			r.Post("/modules", a.apiCreateModule)
			r.Put("/modules/{id}", a.apiUpdateModule)
		})
	})
	r.Handle("/*", dashboard)
	return r
}

const maxDefinitionBytes = 4 << 20

// CARoutes is the LAN listener's handler: only the certificate, for devices
// that cannot reach the API address before they trust anything.
func CARoutes(der []byte) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer, middleware.GetHead)
	certificateRoutes(r, der)
	return r
}

// certificateRoutes serve the same paths on both listeners: DER for the
// platform installers, PEM for command-line tools.
func certificateRoutes(r chi.Router, der []byte) {
	r.Get("/ca.crt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-x509-ca-cert")
		w.Header().Set("Content-Disposition", `attachment; filename="mihomo-capture-ca.crt"`)
		_, _ = w.Write(der)
	})
	r.Get("/ca.pem", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-pem-file")
		w.Header().Set("Content-Disposition", `attachment; filename="mihomo-capture-ca.pem"`)
		_ = pem.Encode(w, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	})
}

func (a *App) requireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(a.Config.APIToken)) != 1 {
			apiError(w, 401, errors.New("Bearer token required"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func jsonResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func apiError(w http.ResponseWriter, status int, err error) {
	code := "invalid_request"
	switch status {
	case 401:
		code = "unauthorized"
	case 404:
		code = "not_found"
	case 405:
		code = "method_not_allowed"
	case 409:
		code = "conflict"
	case 500:
		code = "internal_error"
	case 503:
		code = "gateway_unavailable"
	}
	jsonResponse(w, status, map[string]any{"error": map[string]string{"code": code, "message": err.Error()}})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		apiError(w, 400, err)
		return false
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		apiError(w, 400, errors.New("expected one JSON object"))
		return false
	}
	return true
}

func mutationError(w http.ResponseWriter, err error) {
	var definition module.Invalid
	var input invalidInput
	switch {
	case os.IsNotExist(err):
		apiError(w, 404, errors.New("resource not found"))
	case errors.As(err, &definition), errors.As(err, &input):
		apiError(w, 400, err)
	default:
		apiError(w, 409, err)
	}
}

func (a *App) apiStatus(ca *x509.Certificate) http.HandlerFunc {
	caInfo := map[string]any{"name": ca.Subject.CommonName, "fingerprint": a.Fingerprint, "not_after": ca.NotAfter.UTC(), "lan_url": caLANURL(a.Config.CAListen)}
	return func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		var capture *Capture
		if a.capture != nil {
			c := *a.capture
			capture = &c
		}
		syncErr := a.lastSyncError
		a.mu.Unlock()
		a.store.mu.Lock()
		storage := map[string]any{"records": len(a.store.index), "bytes": a.store.bytes, "max_records": a.Config.MaxRecords, "max_bytes": a.Config.MaxDiskBytes, "retention_hours": a.Config.RetentionHours, "last_error": a.store.lastError}
		a.store.mu.Unlock()
		var gatewayState any
		if a.gate != nil {
			status, err := a.gate.Status(r.Context())
			if err != nil {
				syncErr = err.Error()
			} else {
				gatewayState = status
			}
		}
		jsonResponse(w, 200, map[string]any{"api_version": 3, "capture": capture, "ca": caInfo, "gateway": gatewayState, "gateway_error": syncErr, "storage": storage, "capabilities": map[string]any{"http": true, "https": true, "http2": true, "websocket_frames": true, "modules": []string{"header_rewrites", "url_rewrites", "map_locals", "body_rewrites", "scripts"}, "trust_detection": "operator-attestation", "body_capture": "bounded-wire-bytes", "quic": "optional-udp-port-block-on-selected-device"}})
	}
}

// caLANURL is where devices on the LAN download the certificate, when the
// listener names a concrete address they can reach.
func caLANURL(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if ip := net.ParseIP(host); err != nil || ip == nil || ip.IsUnspecified() {
		return ""
	}
	return "http://" + net.JoinHostPort(host, port) + "/ca.crt"
}

func (a *App) apiDevices(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	d := a.devicesLocked()
	a.mu.Unlock()
	jsonResponse(w, 200, map[string]any{"items": d})
}

func (a *App) apiRefreshDevices(w http.ResponseWriter, r *http.Request) {
	if err := a.RefreshDevices(r.Context()); err != nil {
		apiError(w, 503, err)
		return
	}
	jsonResponse(w, 200, map[string]any{"ok": true})
}

func (a *App) apiRenameDevice(w http.ResponseWriter, r *http.Request) {
	var v struct {
		Label string `json:"label"`
	}
	if !decode(w, r, &v) {
		return
	}
	if err := a.UpdateDevice(r.Context(), r.PathValue("id"), v.Label); err != nil {
		mutationError(w, err)
		return
	}
	jsonResponse(w, 200, map[string]any{"ok": true})
}

func (a *App) apiTrust(w http.ResponseWriter, r *http.Request) {
	var v struct {
		Trusted  *bool  `json:"trusted"`
		Evidence string `json:"trust_evidence"`
	}
	if !decode(w, r, &v) {
		return
	}
	if v.Trusted == nil {
		apiError(w, 400, errors.New("trusted is required"))
		return
	}
	if err := a.Trust(r.Context(), r.PathValue("id"), *v.Trusted, v.Evidence); err != nil {
		mutationError(w, err)
		return
	}
	jsonResponse(w, 200, map[string]any{"ok": true})
}

func (a *App) apiStartCapture(w http.ResponseWriter, r *http.Request) {
	var v struct {
		DeviceID      string   `json:"device_id"`
		BlockUDPPorts []uint16 `json:"block_udp_ports"`
	}
	if !decode(w, r, &v) {
		return
	}
	c, err := a.StartCapture(r.Context(), v.DeviceID, v.BlockUDPPorts)
	if err != nil {
		mutationError(w, err)
		return
	}
	jsonResponse(w, 200, c)
}

func (a *App) apiStopCapture(w http.ResponseWriter, r *http.Request) {
	if err := a.StopCapture(r.Context()); err != nil {
		apiError(w, 503, err)
		return
	}
	jsonResponse(w, 200, map[string]any{"ok": true})
}

func (a *App) apiModules(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, 200, map[string]any{"items": a.Modules()})
}

func (a *App) apiModule(w http.ResponseWriter, r *http.Request) {
	m, err := a.Module(r.PathValue("id"))
	if err != nil {
		mutationError(w, err)
		return
	}
	jsonResponse(w, 200, m)
}

func (a *App) apiCreateModule(w http.ResponseWriter, r *http.Request) {
	var d module.Definition
	if !decode(w, r, &d) {
		return
	}
	m, err := a.CreateModule(d)
	if err != nil {
		mutationError(w, err)
		return
	}
	jsonResponse(w, 201, m)
}

func (a *App) apiUpdateModule(w http.ResponseWriter, r *http.Request) {
	var d module.Definition
	if !decode(w, r, &d) {
		return
	}
	m, err := a.UpdateModule(r.Context(), r.PathValue("id"), d)
	if err != nil {
		mutationError(w, err)
		return
	}
	jsonResponse(w, 200, m)
}

func (a *App) apiActivateModule(w http.ResponseWriter, r *http.Request) {
	var v struct {
		Enabled *bool     `json:"enabled"`
		Devices *[]string `json:"devices"`
	}
	if !decode(w, r, &v) {
		return
	}
	if v.Enabled == nil {
		apiError(w, 400, errors.New("enabled is required"))
		return
	}
	m, err := a.ActivateModule(r.Context(), r.PathValue("id"), *v.Enabled, v.Devices)
	if err != nil {
		mutationError(w, err)
		return
	}
	jsonResponse(w, 200, m)
}

func (a *App) apiModuleLogs(w http.ResponseWriter, r *http.Request) {
	logs, err := a.ModuleLogs(r.PathValue("id"))
	if err != nil {
		mutationError(w, err)
		return
	}
	jsonResponse(w, 200, map[string]any{"items": logs})
}

func (a *App) apiRemoveModule(w http.ResponseWriter, r *http.Request) {
	if err := a.RemoveModule(r.Context(), r.PathValue("id")); err != nil {
		mutationError(w, err)
		return
	}
	jsonResponse(w, 200, map[string]any{"ok": true})
}

func (a *App) apiRecords(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	status, limit := 0, 50
	var err error
	if v := q.Get("status"); v != "" {
		status, err = strconv.Atoi(v)
		if err != nil || status < 100 || status > 599 {
			apiError(w, 400, errors.New("status must be 100..599"))
			return
		}
	}
	if v := q.Get("limit"); v != "" {
		limit, err = strconv.Atoi(v)
		if err != nil || limit < 1 || limit > 200 {
			apiError(w, 400, errors.New("limit must be 1..200"))
			return
		}
	}
	device := q.Get("device")
	if device != "" {
		if device, err = a.deviceID(device); err != nil {
			apiError(w, 400, errors.New("device must be a device ID"))
			return
		}
	}
	items, next, err := a.store.List(Filter{Host: q.Get("host"), Path: q.Get("path"), Method: q.Get("method"), Keyword: q.Get("q"), Device: device, Status: status, Limit: limit, Before: q.Get("before")})
	if err != nil {
		apiError(w, 500, err)
		return
	}
	jsonResponse(w, 200, map[string]any{"items": items, "next": next})
}

func (a *App) apiRecord(w http.ResponseWriter, r *http.Request) {
	v, err := a.store.Get(r.PathValue("id"))
	if err != nil {
		mutationError(w, err)
		return
	}
	jsonResponse(w, 200, v)
}

func (a *App) apiClearRecords(w http.ResponseWriter, r *http.Request) {
	if err := a.ClearRecords(); err != nil {
		apiError(w, 500, err)
		return
	}
	jsonResponse(w, 200, map[string]any{"ok": true})
}
