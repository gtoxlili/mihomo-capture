package web

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// The dashboard displays bodies from arbitrary sites next to the API token.
// Whatever it serves, built or not, must forbid inline and foreign script and
// any connection off the service's own origin.
func TestDashboardContentSecurityPolicy(t *testing.T) {
	h := Handler()
	for _, path := range []string{"/", "/records", "/assets/missing.js"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		directives := map[string]string{}
		for _, d := range strings.Split(rec.Header().Get("Content-Security-Policy"), ";") {
			name, value, _ := strings.Cut(strings.TrimSpace(d), " ")
			directives[name] = value
		}
		if directives["script-src"] != "'self'" || directives["connect-src"] != "'self'" || directives["default-src"] != "'self'" || directives["frame-ancestors"] != "'none'" {
			t.Fatalf("%s: policy %q", path, rec.Header().Get("Content-Security-Policy"))
		}
	}
}
