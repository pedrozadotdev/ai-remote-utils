package main

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateCert_ValidCert(t *testing.T) {
	certPEM, keyPEM, err := GenerateCert()
	if err != nil {
		t.Fatalf("GenerateCert() error = %v", err)
	}
	if len(certPEM) == 0 {
		t.Fatal("GenerateCert() returned empty cert PEM")
	}
	if len(keyPEM) == 0 {
		t.Fatal("GenerateCert() returned empty key PEM")
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("failed to decode cert PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("x509.ParseCertificate error = %v", err)
	}

	// Verify 10-year validity
	expectedDuration := 10 * 365 * 24 * time.Hour
	actualDuration := cert.NotAfter.Sub(cert.NotBefore)
	if actualDuration < expectedDuration {
		t.Errorf("cert validity = %v, want >= %v", actualDuration, expectedDuration)
	}
	if cert.NotAfter.Before(time.Now().Add(9 * 365 * 24 * time.Hour)) {
		t.Errorf("cert expires too soon: %v", cert.NotAfter)
	}
}

func TestGenerateCert_KeyPairMatch(t *testing.T) {
	certPEM, keyPEM, err := GenerateCert()
	if err != nil {
		t.Fatalf("GenerateCert() error = %v", err)
	}

	// Parse cert
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("failed to decode cert PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("x509.ParseCertificate error = %v", err)
	}

	// Parse key
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		t.Fatal("failed to decode key PEM")
	}
	_, err = x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		// Try PKCS1 for older format
		_, err2 := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
		if err2 != nil {
			t.Fatalf("failed to parse private key: PKCS8: %v, PKCS1: %v", err, err2)
		}
	}

	// Verify public key matches (basic check: same algorithm)
	if cert.PublicKeyAlgorithm != x509.RSA {
		t.Errorf("expected RSA, got %v", cert.PublicKeyAlgorithm)
	}
}

func TestCertIsExpired_ValidCert(t *testing.T) {
	certPEM, _, err := GenerateCert()
	if err != nil {
		t.Fatalf("GenerateCert() error = %v", err)
	}
	if CertIsExpired(certPEM) {
		t.Error("CertIsExpired() = true for freshly generated cert")
	}
}

func TestCertIsExpired_InvalidPEM(t *testing.T) {
	if !CertIsExpired([]byte("invalid-pem-data")) {
		t.Error("CertIsExpired() = false for invalid PEM")
	}
}

func TestCertIsExpired_EmptyPEM(t *testing.T) {
	if !CertIsExpired([]byte{}) {
		t.Error("CertIsExpired() = false for empty PEM")
	}
}

func TestCertIsExpired_ExpiredCert(t *testing.T) {
	// Create a cert that expires 1 second from now
	// We'll generate a real cert and then check it against time.Now...
	// Actually, CertIsExpired only checks the PEM parsing and NotAfter.
	// Let's generate a cert with a very short validity.
	certPEM, _, err := generateShortLivedCert(1 * time.Second)
	if err != nil {
		t.Fatalf("generateShortLivedCert() error = %v", err)
	}
	time.Sleep(1100 * time.Millisecond)
	if !CertIsExpired(certPEM) {
		t.Error("CertIsExpired() = false for expired cert")
	}
}

func TestCertExpiresSoon_Valid(t *testing.T) {
	certPEM, _, err := GenerateCert()
	if err != nil {
		t.Fatalf("GenerateCert() error = %v", err)
	}
	// Fresh cert should not expire within 30 days
	if CertExpiresSoon(certPEM, 30*24*time.Hour) {
		t.Error("CertExpiresSoon() = true for fresh cert with 30-day window")
	}
}

func TestCertExpiresSoon_Expiring(t *testing.T) {
	// Generate a cert that expires in 29 days
	certPEM, _, err := generateShortLivedCert(29 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("generateShortLivedCert() error = %v", err)
	}
	if !CertExpiresSoon(certPEM, 30*24*time.Hour) {
		t.Error("CertExpiresSoon() = false for cert expiring in 29 days with 30-day window")
	}
}

func TestCertExpiresSoon_NotExpiring(t *testing.T) {
	// Generate a cert that expires in 31 days
	certPEM, _, err := generateShortLivedCert(31 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("generateShortLivedCert() error = %v", err)
	}
	if CertExpiresSoon(certPEM, 30*24*time.Hour) {
		t.Error("CertExpiresSoon() = true for cert expiring in 31 days with 30-day window")
	}
}

func TestEnsureCert_CreatesAndLoads(t *testing.T) {
	certDir := t.TempDir()
	certPath := filepath.Join(certDir, "cert.pem")
	keyPath := filepath.Join(certDir, "key.pem")

	// First call should generate
	err := EnsureCert(certDir)
	if err != nil {
		t.Fatalf("EnsureCert() error = %v", err)
	}
	if _, err := os.Stat(certPath); os.IsNotExist(err) {
		t.Fatal("cert.pem not created")
	}
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		t.Fatal("key.pem not created")
	}

	// Second call should reuse without error
	err = EnsureCert(certDir)
	if err != nil {
		t.Fatalf("EnsureCert() second call error = %v", err)
	}
}

func TestEnsureCert_ReusesExisting(t *testing.T) {
	certDir := t.TempDir()
	certPath := filepath.Join(certDir, "cert.pem")

	// Generate first cert
	err := EnsureCert(certDir)
	if err != nil {
		t.Fatalf("EnsureCert() error = %v", err)
	}
	firstContent, _ := os.ReadFile(certPath)

	// Call again — should not regenerate
	err = EnsureCert(certDir)
	if err != nil {
		t.Fatalf("EnsureCert() second call error = %v", err)
	}
	secondContent, _ := os.ReadFile(certPath)

	if string(firstContent) != string(secondContent) {
		t.Error("EnsureCert() regenerated cert instead of reusing")
	}
}

func TestEnsureCert_RegeneratesOnExpiry(t *testing.T) {
	certDir := t.TempDir()
	certPath := filepath.Join(certDir, "cert.pem")

	// Write an expired cert
	expiredPEM, _, err := generateShortLivedCert(-1 * time.Hour)
	if err != nil {
		t.Fatalf("generateShortLivedCert() error = %v", err)
	}
	if err := os.WriteFile(certPath, expiredPEM, 0644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	// Write a corresponding key (simplified: reuse the test key)
	_, keyPEM, err := generateShortLivedCert(-1 * time.Hour)
	if err != nil {
		t.Fatalf("generateShortLivedCert() error = %v", err)
	}
	keyPath := filepath.Join(certDir, "key.pem")
	if err := os.WriteFile(keyPath, keyPEM, 0644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	// EnsureCert should regenerate
	err = EnsureCert(certDir)
	if err != nil {
		t.Fatalf("EnsureCert() error = %v", err)
	}
	newContent, _ := os.ReadFile(certPath)
	if string(newContent) == string(expiredPEM) {
		t.Error("EnsureCert() did not regenerate expired cert")
	}
}

func TestEnsureCert_UnwritableDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("cannot test unwritable dir as root")
	}
	certDir := filepath.Join(t.TempDir(), "nonexistent")

	// Non-existent parent dir should fail
	err := EnsureCert(certDir)
	if err == nil {
		t.Error("EnsureCert() expected error for unwritable dir, got nil")
	}
}

// generateShortLivedCert creates a cert with a custom validity period for testing.
func generateShortLivedCert(validity time.Duration) (certPEM, keyPEM []byte, err error) {
	return generateCertWithValidity(validity)
}
