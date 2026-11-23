package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// GenerateCert creates a self-signed RSA 2048-bit certificate valid for 10 years
// with SAN for all IPs (0.0.0.0). Returns PEM-encoded cert and key.
func GenerateCert() (certPEM, keyPEM []byte, err error) {
	return generateCertWithValidity(10 * 365 * 24 * time.Hour)
}

// generateCertWithValidity creates a cert with a custom validity period (for testing).
func generateCertWithValidity(validity time.Duration) (certPEM, keyPEM []byte, err error) {
	// Generate RSA key pair
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	// Create serial number
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate serial number: %w", err)
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"aru"},
			CommonName:   "aru",
		},
		NotBefore:             now,
		NotAfter:              now.Add(validity),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("0.0.0.0")},
		DNSNames:              []string{"localhost", "*.test", "tmp.test"},
	}

	// Create self-signed certificate
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create certificate: %w", err)
	}

	// Encode cert to PEM
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	if certPEM == nil {
		return nil, nil, fmt.Errorf("failed to encode cert PEM")
	}

	// Encode key to PEM
	keyBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal private key: %w", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes})
	if keyPEM == nil {
		return nil, nil, fmt.Errorf("failed to encode key PEM")
	}

	return certPEM, keyPEM, nil
}

// CertIsExpired returns true if the PEM-encoded certificate is expired or unparseable.
func CertIsExpired(certPEM []byte) bool {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return true // invalid PEM
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return true // unparseable
	}
	return time.Now().After(cert.NotAfter)
}

// CertExpiresSoon returns true if the certificate expires within the given duration.
func CertExpiresSoon(certPEM []byte, within time.Duration) bool {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return true // invalid PEM, treat as expired
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return true
	}
	return time.Now().Add(within).After(cert.NotAfter)
}

// certHasSANs checks if a PEM-encoded certificate contains all the required DNS SANs.
func certHasSANs(certPEM []byte, required []string) bool {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}

	sanSet := make(map[string]bool, len(cert.DNSNames))
	for _, name := range cert.DNSNames {
		sanSet[name] = true
	}

	for _, required := range required {
		if !sanSet[required] {
			return false
		}
	}
	return true
}

// EnsureCert ensures a usable certificate pair exists at certDir and returns the
// DNS suffix aru should route on.
//
// A certificate supplied by the operator is authoritative: aru adopts the suffix
// of its first wildcard DNS SAN (for example "*.example.com" -> "example.com")
// and never rewrites files it did not generate. When no certificate pair is
// present, aru generates a self-signed *.test certificate and returns "test".
func EnsureCert(certDir string) (string, error) {
	// Create cert directory if needed
	if err := os.MkdirAll(certDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create cert directory %s: %w", certDir, err)
	}

	certPath := filepath.Join(certDir, "cert.pem")
	keyPath := filepath.Join(certDir, "key.pem")

	baseDomain, regenerate, err := inspectExistingCert(certPath, keyPath)
	if err != nil {
		return "", err
	}
	if !regenerate {
		return baseDomain, nil
	}

	return generateSelfSignedCert(certPath, keyPath)
}

// inspectExistingCert decides what to do with the certificate pair already on
// disk. It either returns the suffix to adopt, or regenerate=true when aru may
// replace the pair with a fresh self-signed *.test certificate.
func inspectExistingCert(certPath, keyPath string) (baseDomain string, regenerate bool, err error) {
	existingCert, certErr := os.ReadFile(certPath)
	existingKey, keyErr := os.ReadFile(keyPath)

	switch {
	case certErr == nil && keyErr == nil:
		// both halves are on disk: inspect them below
	case certErr == nil:
		return "", false, fmt.Errorf("found %s but no %s: restore key.pem, or remove cert.pem so aru can generate a *.test certificate", certPath, keyPath)
	case keyErr == nil:
		return "", false, fmt.Errorf("found %s but no %s: restore cert.pem, or remove key.pem so aru can generate a *.test certificate", keyPath, certPath)
	default:
		return "", true, nil
	}

	if _, err := tls.X509KeyPair(existingCert, existingKey); err != nil {
		return "", false, fmt.Errorf("certificate %s and key %s are not a valid TLS pair: %w", certPath, keyPath, err)
	}

	aruGenerated := certIsAruGenerated(existingCert)
	if CertIsExpired(existingCert) {
		// aru only ever replaces certificates it generated itself
		if !aruGenerated {
			return "", false, fmt.Errorf("certificate %s is expired: replace cert.pem and key.pem with a valid wildcard certificate", certPath)
		}
		slog.Info("certificate expired, regenerating", "path", certPath)
		return "", true, removeCertPair(certPath, keyPath)
	}

	baseDomain = wildcardBaseDomain(existingCert)
	if baseDomain == "" {
		return "", false, fmt.Errorf("certificate %s has no wildcard DNS SAN: add one such as \"*.example.com\", or remove cert.pem and key.pem so aru generates a *.test certificate", certPath)
	}

	if aruGenerated && !certHasSANs(existingCert, requiredSANs(baseDomain)) {
		slog.Info("certificate missing required SANs, regenerating", "path", certPath)
		return "", true, removeCertPair(certPath, keyPath)
	}

	if CertExpiresSoon(existingCert, 30*24*time.Hour) {
		slog.Warn("certificate expires within 30 days", "path", certPath)
	}
	slog.Debug("reusing existing certificate", "path", certPath, "domain", baseDomain)
	return baseDomain, false, nil
}

// requiredSANs lists the DNS names an aru-generated certificate must carry for
// the given suffix.
func requiredSANs(baseDomain string) []string {
	return []string{"*." + baseDomain, uploadHost(baseDomain), "localhost"}
}

// certIsAruGenerated reports whether the certificate was created by aru, which
// is the only case where aru may replace it on disk.
func certIsAruGenerated(certPEM []byte) bool {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	if cert.Subject.CommonName != "aru" {
		return false
	}
	for _, org := range cert.Subject.Organization {
		if org == "aru" {
			return true
		}
	}
	return false
}

// removeCertPair removes a certificate and its key, ignoring missing files.
func removeCertPair(certPath, keyPath string) error {
	for _, path := range []string{certPath, keyPath} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove %s: %w", path, err)
		}
	}
	return nil
}

// generateSelfSignedCert writes a fresh self-signed *.test pair to disk.
func generateSelfSignedCert(certPath, keyPath string) (string, error) {
	slog.Info("generating self-signed certificate", "path", certPath)
	certPEM, keyPEM, err := GenerateCert()
	if err != nil {
		return "", fmt.Errorf("failed to generate certificate: %w", err)
	}

	if err := os.WriteFile(certPath, certPEM, 0644); err != nil {
		return "", fmt.Errorf("failed to write cert.pem: %w", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return "", fmt.Errorf("failed to write key.pem: %w", err)
	}

	slog.Info("self-signed certificate generated", "path", certPath)
	return defaultBaseDomain, nil
}
