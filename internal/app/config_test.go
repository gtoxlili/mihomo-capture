package app

import (
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// contract: config.example.yaml — identity files resolve beside
// the configuration, are generated on this machine by -init, stay owner-only,
// and a rerun never replaces a CA that devices already trust.
// upstream: Apple, Requirements for trusted certificates in iOS 13 and macOS
// 10.15 (support.apple.com/en-us/103769) — server certificates need the name
// in SAN, serverAuth, and a validity of at most 825 days.
func TestInitIdentity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	config := "ca_cert_file: ../secrets/ca.pem\nca_key_file: ../secrets/ca-key.pem\napi_token_file: ../secrets/api-token\nproxy_token_file: ../secrets/proxy-token\n"
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	created, err := c.InitIdentity()
	if err != nil || len(created) != 4 {
		t.Fatalf("init created %v: %v", created, err)
	}
	ca, fingerprint, err := c.LoadIdentity()
	if err != nil {
		t.Fatal(err)
	}
	// The user chose this name for the CA devices are asked to trust.
	if ca.Leaf.Subject.CommonName != "Mihomo Capture CA" {
		t.Fatalf("CA subject %q", ca.Leaf.Subject.CommonName)
	}
	p, err := NewProxy(&App{Config: c}, ca)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := p.certificate("device.example.com")
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(issued.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca.Leaf)
	if _, err = leaf.Verify(x509.VerifyOptions{DNSName: "device.example.com", Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		t.Fatal("issued certificate does not chain to the generated CA: ", err)
	}
	if leaf.NotAfter.Sub(leaf.NotBefore) > 825*24*time.Hour {
		t.Fatal("issued certificate outlives Apple's limit")
	}
	if created, err = c.InitIdentity(); err != nil || len(created) != 0 {
		t.Fatalf("rerun created %v: %v", created, err)
	}
	if _, again, err := c.LoadIdentity(); err != nil || again != fingerprint {
		t.Fatal("rerun replaced the CA")
	}
	if err = os.Chmod(c.CAKeyFile, 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err = c.LoadIdentity(); err == nil {
		t.Fatal("world-readable private key accepted")
	}
}
