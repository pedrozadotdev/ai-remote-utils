package main

import (
	"crypto/rand"
	"crypto/rsa"
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
			Organization: []string{"tmp-file"},
			CommonName:   "tmp-file",
		},
		NotBefore:             now,
		NotAfter:              now.Add(validity),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("0.0.0.0")},
		DNSNames:              []string{"localhost"},
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

// EnsureCert ensures the certificate exists at certDir, generating if needed.
// If the existing cert is expired, it regenerates. Default certDir is ~/.tmp-file/.
func EnsureCert(certDir string) error {
	// Create cert directory if needed
	if err := os.MkdirAll(certDir, 0700); err != nil {
		return fmt.Errorf("failed to create cert directory %s: %w", certDir, err)
	}

	certPath := filepath.Join(certDir, "cert.pem")
	keyPath := filepath.Join(certDir, "key.pem")

	// Check if both files exist and cert is valid
	if existingCert, err := os.ReadFile(certPath); err == nil {
		if _, err := os.ReadFile(keyPath); err == nil {
			if !CertIsExpired(existingCert) {
				// Cert is valid — reuse
				slog.Debug("reusing existing certificate", "path", certPath)
				return nil
			}
			slog.Info("certificate expired, regenerating", "path", certPath)
			// Check if it's expiring soon and log warning
			if CertExpiresSoon(existingCert, 30*24*time.Hour) {
				slog.Warn("certificate expires within 30 days", "path", certPath)
			}
			// Remove expired files so we can regenerate
			os.Remove(certPath)
			os.Remove(keyPath)
		}
	}

	slog.Info("generating self-signed certificate", "dir", certDir)
	certPEM, keyPEM, err := GenerateCert()
	if err != nil {
		return fmt.Errorf("failed to generate certificate: %w", err)
	}

	if err := os.WriteFile(certPath, certPEM, 0644); err != nil {
		return fmt.Errorf("failed to write cert.pem: %w", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return fmt.Errorf("failed to write key.pem: %w", err)
	}

	slog.Info("self-signed certificate generated", "path", certPath)
	return nil
}
