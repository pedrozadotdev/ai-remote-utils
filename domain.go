package main

import (
	"crypto/x509"
	"encoding/pem"
	"strings"
)

// defaultBaseDomain is the DNS suffix aru routes on when it generates its own
// self-signed certificate.
const defaultBaseDomain = "test"

// normalizeBaseDomain lowercases a DNS suffix and trims a trailing root dot.
func normalizeBaseDomain(domain string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
}

// hasBaseDomain reports whether host is a subdomain of baseDomain, for example
// "app.example.com" for "example.com". The bare suffix itself does not match.
func hasBaseDomain(host, baseDomain string) bool {
	base := normalizeBaseDomain(baseDomain)
	if base == "" {
		return false
	}
	return strings.HasSuffix(strings.ToLower(host), "."+base)
}

// uploadHost returns the hostname that serves the upload form and static UI.
func uploadHost(baseDomain string) string {
	return "tmp." + normalizeBaseDomain(baseDomain)
}

// wildcardBaseDomain returns the suffix of the first wildcard DNS SAN in the
// certificate, for example "example.com" for "*.example.com". It returns ""
// when the PEM is unparseable or carries no wildcard SAN.
func wildcardBaseDomain(certPEM []byte) string {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return ""
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return ""
	}
	for _, name := range cert.DNSNames {
		if base := wildcardSuffix(name); base != "" {
			return base
		}
	}
	return ""
}

// wildcardSuffix returns the base domain of a "*.suffix" SAN, or "".
func wildcardSuffix(name string) string {
	if !strings.HasPrefix(name, "*.") {
		return ""
	}
	base := normalizeBaseDomain(strings.TrimPrefix(name, "*."))
	if base == "" || strings.ContainsAny(base, "/ \t") {
		return ""
	}
	return base
}
