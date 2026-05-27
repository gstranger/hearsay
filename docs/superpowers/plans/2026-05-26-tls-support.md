# TLS/HTTPS Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add TLS/HTTPS to REST and A2A servers with auto-generated self-signed certs and user-provided cert support.

**Architecture:** Add `--tls-cert`, `--tls-key`, `--tls-auto` flags. Create `internal/server/tls.go` helper for self-signed cert generation using `crypto/x509` and `crypto/rsa`. Wire TLS into both REST and A2A server startup in `cmd/hearsay/main.go`.

**Tech Stack:** Go 1.25 standard library (`crypto/x509`, `crypto/rsa`, `crypto/rand`, `encoding/pem`)

---

### File Mapping

| File | Responsibility |
|---|---|
| `internal/server/tls.go` | **NEW** — Self-signed cert generation helper |
| `internal/server/tls_test.go` | **NEW** — Tests for cert generation |
| `cmd/hearsay/main.go` | Add `--tls-*` flags; wire `ListenAndServeTLS` for REST and A2A |
| `README.md` | Document TLS flags |

---

### Task 1: Create TLS Cert Generation Helper

**Files:**
- Create: `internal/server/tls.go`
- Create: `internal/server/tls_test.go`

- [ ] **Step 1: Write `internal/server/tls.go`**

```go
package server

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"time"
)

const (
	autoCertFile = ".hearsay.crt"
	autoKeyFile  = ".hearsay.key"
)

// GenerateSelfSignedCert creates a self-signed TLS certificate and key.
// If certPath/keyPath are empty, it uses .hearsay.crt/.hearsay.key in the working directory.
// Returns the actual paths used.
func GenerateSelfSignedCert(certPath, keyPath string) (string, string, error) {
	if certPath == "" {
		certPath = autoCertFile
	}
	if keyPath == "" {
		keyPath = autoKeyFile
	}

	// Check if already exists
	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			return certPath, keyPath, nil
		}
	}

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", fmt.Errorf("generate key: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"hearsay-local"},
			CommonName:   "hearsay-local",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		DNSNames:              []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return "", "", fmt.Errorf("create certificate: %w", err)
	}

	certOut, err := os.Create(certPath)
	if err != nil {
		return "", "", fmt.Errorf("create cert file: %w", err)
	}
	pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	certOut.Close()

	keyOut, err := os.Create(keyPath)
	if err != nil {
		return "", "", fmt.Errorf("create key file: %w", err)
	}
	pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	keyOut.Close()

	return certPath, keyPath, nil
}
```

- [ ] **Step 2: Write `internal/server/tls_test.go`**

```go
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
```

- [ ] **Step 3: Run tests**

```bash
cd ~/Documents/agentstate
go test ./internal/server/... -run TestGenerateSelfSignedCert -v
```

Expected: 2 PASS

- [ ] **Step 4: Commit**

```bash
git add internal/server/tls.go internal/server/tls_test.go
git commit -m "feat: add self-signed TLS certificate generation helper"
```

---

### Task 2: Wire TLS Flags into CLI

**Files:**
- Modify: `cmd/hearsay/main.go`

- [ ] **Step 1: Add TLS flags to `cmdServe`**

After existing flags, add:

```go
tlsCert := fs.String("tls-cert", "", "Path to TLS certificate file")
tlsKey := fs.String("tls-key", "", "Path to TLS private key file")
tlsAuto := fs.Bool("tls-auto", false, "Auto-generate self-signed TLS certificate")
```

- [ ] **Step 2: Auto-generate cert if `--tls-auto` set**

After `fs.Parse(args)`, add:

```go
if *tlsAuto {
	if *tlsCert == "" && *tlsKey == "" {
		cert, key, err := server.GenerateSelfSignedCert("", "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error generating TLS cert: %v\n", err)
			os.Exit(1)
		}
		*tlsCert = cert
		*tlsKey = key
		log.Printf("Auto-generated TLS certificate: %s", cert)
	}
}
```

- [ ] **Step 3: Update REST server startup to use TLS when configured**

Replace:
```go
log.Printf("Listening on %s", *addr)
if err := httpSrv.ListenAndServe(); err != http.ErrServerClosed {
    log.Fatalf("server error: %v", err)
}
```

With:
```go
if *tlsCert != "" && *tlsKey != "" {
	log.Printf("Listening on %s (HTTPS)", *addr)
	if err := httpSrv.ListenAndServeTLS(*tlsCert, *tlsKey); err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
} else {
	log.Printf("Listening on %s (HTTP)", *addr)
	if err := httpSrv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}
```

- [ ] **Step 4: Update A2A server startup to use TLS when configured**

Same pattern for A2A server:

```go
if *tlsCert != "" && *tlsKey != "" {
	go func() {
		log.Printf("A2A server listening on %s (HTTPS)", a2aCfg.Addr)
		if err := a2aHttpSrv.ListenAndServeTLS(*tlsCert, *tlsKey); err != nil && err != http.ErrServerClosed {
			log.Printf("A2A server error: %v", err)
		}
	}()
} else {
	go func() {
		log.Printf("A2A server listening on %s (HTTP)", a2aCfg.Addr)
		if err := a2aHttpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("A2A server error: %v", err)
		}
	}()
}
```

- [ ] **Step 5: Add `server` import if needed**

Ensure `github.com/gstranger/hearsay/internal/server` is imported.

- [ ] **Step 6: Build and test**

```bash
cd ~/Documents/agentstate
go build ./cmd/hearsay
```

Expected: builds successfully

```bash
go test ./...
```

Expected: all 11 packages pass

- [ ] **Step 7: Commit**

```bash
git add cmd/hearsay/main.go
git commit -m "feat: add --tls-cert, --tls-key, --tls-auto flags for HTTPS"
```

---

### Task 3: Update README

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update Quick Start server examples**

Replace the existing server examples with:

```bash
# REST API only (default port 8080, HTTP)
hearsay serve

# With HTTPS using auto-generated self-signed cert
hearsay serve --tls-auto

# With HTTPS using your own cert
hearsay serve --tls-cert server.crt --tls-key server.key

# With authentication, structured logging, and HTTPS
hearsay serve --auth-token my-secret-key --log-format json --tls-auto

# With A2A server on separate port (HTTPS)
hearsay serve --a2a-addr localhost:8081 --a2a-api-key my-secret-key --tls-auto
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: document TLS/HTTPS flags"
```

---

### Task 4: Final Verification

- [ ] **Step 1: Run full test suite**

```bash
cd ~/Documents/agentstate
go test ./...
```

Expected: all 11 packages pass

- [ ] **Step 2: Build binary**

```bash
go build ./cmd/hearsay
```

Expected: creates `./hearsay` binary

- [ ] **Step 3: Test help output shows TLS flags**

```bash
./hearsay serve --help 2>&1 | grep -E "tls"
```

Expected: shows `--tls-cert`, `--tls-key`, `--tls-auto`

- [ ] **Step 4: Test auto-generated cert**

```bash
# Clean up any existing auto-certs
rm -f .hearsay.crt .hearsay.key

# Start server with auto TLS
./hearsay serve --tls-auto --auth-token secret &
SERVER_PID=$!
sleep 2

# Verify cert files were created
ls -la .hearsay.crt .hearsay.key

# Test HTTPS (curl -k to skip cert verification)
curl -k -s https://localhost:8080/health
# Expected: {"status":"ok"}

# Test that HTTP is NOT serving (or verify HTTPS works)
curl -k -s -o /dev/null -w "%{http_code}" -X POST https://localhost:8080/claim \
  -H "Authorization: Bearer secret" \
  -d '{"agent_id":"a1","resource_uri":"file://x","operation":"write","ttl_seconds":60}'
# Expected: 200 or 409 (not 401)

kill $SERVER_PID
rm -f .hearsay.crt .hearsay.key
```

- [ ] **Step 5: Commit any fixes**

If issues found, fix and commit.

---

## Spec Coverage Check

| Spec Requirement | Task |
|---|---|
| `--tls-cert` flag | Task 2 |
| `--tls-key` flag | Task 2 |
| `--tls-auto` flag | Task 2 |
| Auto-generate self-signed cert | Task 1, Task 2 |
| Reuse existing auto-generated cert | Task 1 |
| TLS on REST server | Task 2 |
| TLS on A2A server | Task 2 |
| README docs | Task 3 |

## Placeholder Scan

No placeholders. All steps contain exact code and commands.

## Type Consistency Check

- `GenerateSelfSignedCert(certPath, keyPath string) (string, string, error)` — used in Task 1 and Task 2
- `autoCertFile = ".hearsay.crt"`, `autoKeyFile = ".hearsay.key"` — consistent
