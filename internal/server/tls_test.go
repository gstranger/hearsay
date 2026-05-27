package server

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"testing"
)

func TestGenerateSelfSignedCert(t *testing.T) {
	certPath := ".test_hearsay.crt"
	keyPath := ".test_hearsay.key"
	defer os.Remove(certPath)
	defer os.Remove(keyPath)

	gotCert, gotKey, err := GenerateSelfSignedCert(certPath, keyPath)
	if err != nil {
		t.Fatalf("generate cert: %v", err)
	}
	if gotCert != certPath {
		t.Fatalf("expected cert path %s, got %s", certPath, gotCert)
	}
	if gotKey != keyPath {
		t.Fatalf("expected key path %s, got %s", keyPath, gotKey)
	}

	// Verify cert file exists and is valid PEM
	certPEM, err := os.ReadFile(gotCert)
	if err != nil {
		t.Fatalf("read cert: %v", err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("failed to decode cert PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	if cert.Subject.CommonName != "hearsay-local" {
		t.Fatalf("expected CN hearsay-local, got %s", cert.Subject.CommonName)
	}

	// Verify key file exists
	keyPEM, err := os.ReadFile(gotKey)
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		t.Fatal("failed to decode key PEM")
	}
}

func TestGenerateSelfSignedCert_ReusesExisting(t *testing.T) {
	certPath := ".test_hearsay2.crt"
	keyPath := ".test_hearsay2.key"
	defer os.Remove(certPath)
	defer os.Remove(keyPath)

	// First call creates
	_, _, err := GenerateSelfSignedCert(certPath, keyPath)
	if err != nil {
		t.Fatalf("first generate: %v", err)
	}

	// Second call should reuse
	_, _, err = GenerateSelfSignedCert(certPath, keyPath)
	if err != nil {
		t.Fatalf("second generate: %v", err)
	}
}
