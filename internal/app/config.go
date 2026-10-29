package app

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// CAName is the subject devices show when asked to trust the capture CA.
const CAName = "Mihomo Capture CA"

type Config struct {
	Mode                string   `yaml:"mode"`             // local (default) or gateway
	LocalSourceIPs      []string `yaml:"local_source_ips"` // additional mihomo source addresses, such as a TUN address
	ControllerURL       string   `yaml:"controller_url"`
	ControllerTokenFile string   `yaml:"controller_token_file"`
	APIListen           string   `yaml:"api_listen"`
	CAListen            string   `yaml:"ca_listen"` // optional LAN address serving only the CA certificate, without authentication
	ProxyListen         string   `yaml:"proxy_listen"`
	EgressListen        string   `yaml:"egress_listen"` // loopback listener the catalog adds to the gateway
	DataDir             string   `yaml:"data_dir"`
	LogFile             string   `yaml:"log_file"`
	ControllerSocket    string   `yaml:"controller_socket"`
	GatewayConfig       string   `yaml:"gateway_config"`    // the file mihomo runs; read, never written
	GatewayStateDir     string   `yaml:"gateway_state_dir"` // lane selector files; must lie under mihomo's home
	DeviceMapFile       string   `yaml:"device_map_file"`
	LAN                 string   `yaml:"lan"`
	ARPUseSudo          bool     `yaml:"arp_use_sudo"`
	APITokenFile        string   `yaml:"api_token_file"`
	ProxyTokenFile      string   `yaml:"proxy_token_file"`
	APIToken            string   `yaml:"-"`
	ProxyToken          string   `yaml:"-"`
	CACertFile          string   `yaml:"ca_cert_file"`     // PEM root certificate, created by -init
	CAKeyFile           string   `yaml:"ca_key_file"`      // PEM private key; generated here, never copied elsewhere
	UpstreamCAFile      string   `yaml:"upstream_ca_file"` // Optional additional roots for internal HTTPS servers.
	MaxDiskBytes        int64    `yaml:"max_disk_bytes"`
	MaxRecords          int      `yaml:"max_records"`
	MaxBodyBytes        int      `yaml:"max_body_bytes"`
	MaxFrameBytes       int      `yaml:"max_frame_bytes"`
	MaxFrames           int      `yaml:"max_frames"`
	RetentionHours      int      `yaml:"retention_hours"`
	MaxConnections      int      `yaml:"max_connections"`
}

func LoadConfig(path string) (Config, error) {
	c := Config{Mode: "local", APIListen: "127.0.0.1:19090", ProxyListen: "127.0.0.1:19091", EgressListen: "127.0.0.1:19092", DataDir: "./data", MaxDiskBytes: 256 << 20, MaxRecords: 5000, MaxBodyBytes: 1 << 20, MaxFrameBytes: 16 << 10, MaxFrames: 128, RetentionHours: 24, MaxConnections: 256}
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	d := yaml.NewDecoder(strings.NewReader(string(b)))
	d.KnownFields(true)
	if err = d.Decode(&c); err != nil {
		return c, err
	}
	if c.MaxDiskBytes < 1<<20 || c.MaxRecords < 1 || c.MaxBodyBytes < 1 || c.MaxBodyBytes > 8<<20 || c.MaxFrames < 1 || c.MaxFrames > 2048 || c.MaxFrameBytes < 1 || c.MaxFrameBytes > 1<<20 || c.RetentionHours < 1 || c.MaxConnections < 1 || c.MaxConnections > 4096 {
		return c, errors.New("invalid retention or resource limits")
	}
	if c.Mode != "local" && c.Mode != "gateway" {
		return c, errors.New("mode must be local or gateway")
	}
	if c.Mode == "gateway" {
		if _, _, err := net.ParseCIDR(c.LAN); err != nil {
			return c, errors.New("gateway mode requires a LAN CIDR")
		}
	}
	for _, ip := range c.LocalSourceIPs {
		if net.ParseIP(ip) == nil {
			return c, fmt.Errorf("invalid local source IP %q", ip)
		}
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return c, err
	}
	for _, p := range []*string{&c.DataDir, &c.LogFile, &c.CACertFile, &c.CAKeyFile, &c.APITokenFile, &c.ProxyTokenFile, &c.ControllerSocket, &c.ControllerTokenFile, &c.GatewayConfig, &c.GatewayStateDir, &c.DeviceMapFile, &c.UpstreamCAFile} {
		if *p != "" && !filepath.IsAbs(*p) {
			*p = filepath.Join(base, *p)
		}
	}
	if c.GatewayStateDir == "" && c.GatewayConfig != "" {
		c.GatewayStateDir = filepath.Join(filepath.Dir(c.GatewayConfig), "mihomo-capture")
	}
	return c, nil
}

func privateFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("private file must be regular and accessible only to its owner: %s", path)
	}
	if st.Size() > 1<<20 {
		return nil, fmt.Errorf("private file exceeds 1 MiB: %s", path)
	}
	return io.ReadAll(io.LimitReader(f, 1<<20))
}

func (c *Config) LoadIdentity() (*tls.Certificate, string, error) {
	a, err := privateFile(c.APITokenFile)
	if err != nil {
		return nil, "", err
	}
	b, err := privateFile(c.ProxyTokenFile)
	if err != nil {
		return nil, "", err
	}
	c.APIToken = strings.TrimSpace(string(a))
	c.ProxyToken = strings.TrimSpace(string(b))
	if len(c.APIToken) < 32 || len(c.ProxyToken) < 32 || c.APIToken == c.ProxyToken {
		return nil, "", errors.New("distinct API and proxy tokens of at least 32 characters required")
	}
	certPEM, err := os.ReadFile(c.CACertFile)
	if err != nil {
		return nil, "", fmt.Errorf("load CA: %w", err)
	}
	keyPEM, err := privateFile(c.CAKeyFile)
	if err != nil {
		return nil, "", fmt.Errorf("load CA: %w", err)
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, "", fmt.Errorf("load CA: %w", err)
	}
	root, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, "", fmt.Errorf("load CA: %w", err)
	}
	now := time.Now()
	if !root.IsCA || root.CheckSignatureFrom(root) != nil || now.Before(root.NotBefore) || !now.Before(root.NotAfter) {
		return nil, "", errors.New("CA must be a valid self-signed certificate authority")
	}
	pair.Leaf = root
	h := sha256.Sum256(root.Raw)
	return &pair, hex.EncodeToString(h[:]), nil
}

// InitIdentity creates the identity files that are missing. The CA key is
// generated on this machine and written nowhere else. Existing files are kept,
// so rerunning the installer cannot replace a CA that devices already trust.
func (c *Config) InitIdentity() ([]string, error) {
	var created []string
	certExists, err := exists(c.CACertFile)
	if err != nil {
		return nil, err
	}
	keyExists, err := exists(c.CAKeyFile)
	if err != nil {
		return nil, err
	}
	if certExists != keyExists {
		return nil, errors.New("ca_cert_file and ca_key_file must both exist or both be absent")
	}
	if !certExists {
		certPEM, keyPEM, err := newCA(time.Now())
		if err != nil {
			return nil, err
		}
		if err = createPrivate(c.CAKeyFile, keyPEM); err != nil {
			return nil, err
		}
		if err = createPrivate(c.CACertFile, certPEM); err != nil {
			return nil, err
		}
		created = append(created, c.CAKeyFile, c.CACertFile)
	}
	for _, path := range []string{c.APITokenFile, c.ProxyTokenFile} {
		ok, err := exists(path)
		if err != nil {
			return created, err
		}
		if ok {
			continue
		}
		b := make([]byte, 32)
		if _, err = rand.Read(b); err != nil {
			return created, err
		}
		if err = createPrivate(path, []byte(base64.RawURLEncoding.EncodeToString(b)+"\n")); err != nil {
			return created, err
		}
		created = append(created, path)
	}
	return created, nil
}

// newCA issues a root that signs leaf certificates directly. ECDSA P-256
// leaves follow from an ECDSA root and are accepted by every current client.
func newCA(now time.Time) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: CAName, Organization: []string{"Mihomo Capture"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}), nil
}

func exists(path string) (bool, error) {
	if path == "" {
		return false, errors.New("identity file paths are required")
	}
	_, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

// createPrivate writes an owner-only file that did not exist; a concurrent or
// earlier file of the same name is never replaced.
func createPrivate(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0o600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Link(f.Name(), path)
	}
	return err
}

// LoadControllerToken reads the optional controller secret with the same
// owner-only file policy as the service identity.
func (c Config) LoadControllerToken() (string, error) {
	if c.ControllerTokenFile == "" {
		return "", nil
	}
	b, err := privateFile(c.ControllerTokenFile)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return "", errors.New("controller token file is empty")
	}
	return token, nil
}
