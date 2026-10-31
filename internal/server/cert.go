package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
)

// WriteDevCert writes a self-signed certificate and key for local development.
// hosts are DNS names or IP addresses. An empty list uses localhost and the loopback addresses.
func WriteDevCert(certPath, keyPath string, hosts []string) error {
	if certPath == "" || keyPath == "" {
		return gerr.New(gerr.InvalidArgument, "cert and key paths are required")
	}
	if len(hosts) == 0 {
		hosts = []string{"localhost", "127.0.0.1", "::1"}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return gerr.Wrap(gerr.Internal, "generate key", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return gerr.Wrap(gerr.Internal, "generate serial", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: hosts[0], Organization: []string{"graphdb"}},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	for _, host := range hosts {
		if ip := net.ParseIP(host); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
			continue
		}
		tmpl.DNSNames = append(tmpl.DNSNames, host)
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return gerr.Wrap(gerr.Internal, "create certificate", err)
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o755); err != nil {
		return gerr.Wrap(gerr.Unavailable, "create cert directory", err)
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o755); err != nil {
		return gerr.Wrap(gerr.Unavailable, "create key directory", err)
	}
	certFile, err := os.OpenFile(certPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return gerr.Wrap(gerr.Unavailable, "create cert file", err)
	}
	if err := pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		_ = certFile.Close()
		return gerr.Wrap(gerr.Unavailable, "write cert", err)
	}
	if err := certFile.Close(); err != nil {
		return gerr.Wrap(gerr.Unavailable, "close cert", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return gerr.Wrap(gerr.Internal, "marshal key", err)
	}
	keyFile, err := os.OpenFile(keyPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return gerr.Wrap(gerr.Unavailable, "create key file", err)
	}
	if err := pem.Encode(keyFile, &pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}); err != nil {
		_ = keyFile.Close()
		return gerr.Wrap(gerr.Unavailable, "write key", err)
	}
	if err := keyFile.Close(); err != nil {
		return gerr.Wrap(gerr.Unavailable, "close key", err)
	}
	return nil
}
