package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// ~100 years
const selfSignedCertValidity = 100 * 365 * 24 * time.Hour

func ensureSelfSignedCert(cacheDir, MAINDomain, proxyDomain string) (certPath, keyPath string, err error) {
	dir := filepath.Join(cacheDir, "self")
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")

	if selfSignedCertUsable(certPath, keyPath) {
		return certPath, keyPath, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	if err := generateSelfSignedCert(certPath, keyPath, MAINDomain, proxyDomain); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}

func selfSignedCertUsable(certPath, keyPath string) bool {
	data, err := os.ReadFile(certPath)
	if err != nil {
		return false
	}
	if _, err := os.Stat(keyPath); err != nil {
		return false
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	return time.Now().Before(cert.NotAfter.Add(-24 * time.Hour))
}

func generateSelfSignedCert(certPath, keyPath, MAINDomain, proxyDomain string) error {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate key: %w", err)
	}

	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return fmt.Errorf("serial: %w", err)
	}

	dnsNames := dedupStrings([]string{
		MAINDomain,
		proxyDomain,
		"*." + proxyDomain,
		"localhost",
	})
	ipAddrs := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}

	now := time.Now()
	tpl := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   MAINDomain,
			Organization: []string{"Proxywi self-signed"},
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(selfSignedCertValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              dnsNames,
		IPAddresses:           ipAddrs,
	}

	der, err := x509.CreateCertificate(rand.Reader, &tpl, &tpl, &priv.PublicKey, priv)
	if err != nil {
		return fmt.Errorf("create cert: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return fmt.Errorf("marshal key: %w", err)
	}

	if err := writePEMFile(certPath, "CERTIFICATE", der, 0o644); err != nil {
		return err
	}
	if err := writePEMFile(keyPath, "EC PRIVATE KEY", keyDER, 0o600); err != nil {
		return err
	}
	return nil
}

func writePEMFile(path, blockType string, der []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: blockType, Bytes: der})
}

func dedupStrings(s []string) []string {
	seen := make(map[string]struct{}, len(s))
	out := make([]string, 0, len(s))
	for _, v := range s {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
