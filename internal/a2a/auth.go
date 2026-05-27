package a2a

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type AuthResult struct {
	AgentID string
	OK      bool
}

type AuthMiddleware struct {
	APIKey             string
	BearerValidatorURL string
	BearerJWKSURL      string
	jwks               *jwksCache
}

type jwksCache struct {
	url       string
	keys      map[string]any // kid -> *rsa.PublicKey or *ecdsa.PublicKey
	mu        sync.RWMutex
	expiresAt time.Time
	client    *http.Client
}

func newJWKSCache(url string) *jwksCache {
	return &jwksCache{
		url:    url,
		keys:   make(map[string]any),
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

type jwksResponse struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	X   string `json:"x"`
	Y   string `json:"y"`
	Crv string `json:"crv"`
}

func (c *jwksCache) getKey(kid string) (any, error) {
	c.mu.RLock()
	if key, ok := c.keys[kid]; ok && time.Now().Before(c.expiresAt) {
		c.mu.RUnlock()
		return key, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if key, ok := c.keys[kid]; ok && time.Now().Before(c.expiresAt) {
		return key, nil
	}

	if err := c.fetch(); err != nil {
		return nil, err
	}

	key, ok := c.keys[kid]
	if !ok {
		return nil, fmt.Errorf("key %q not found in JWKS", kid)
	}
	return key, nil
}

func (c *jwksCache) fetch() error {
	resp, err := c.client.Get(c.url)
	if err != nil {
		return fmt.Errorf("fetch JWKS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch JWKS: status %d", resp.StatusCode)
	}

	var jwks jwksResponse
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("decode JWKS: %w", err)
	}

	newKeys := make(map[string]any)
	for _, k := range jwks.Keys {
		key, err := parseJWK(k)
		if err != nil {
			continue // skip unparseable keys
		}
		newKeys[k.Kid] = key
	}

	c.keys = newKeys
	c.expiresAt = time.Now().Add(5 * time.Minute)
	return nil
}

func parseJWK(k jwk) (any, error) {
	switch k.Kty {
	case "RSA":
		n, err := base64urlDecode(k.N)
		if err != nil {
			return nil, err
		}
		e, err := base64urlDecode(k.E)
		if err != nil {
			return nil, err
		}
		return &rsa.PublicKey{
			N: new(big.Int).SetBytes(n),
			E: int(new(big.Int).SetBytes(e).Int64()),
		}, nil
	case "EC":
		x, err := base64urlDecode(k.X)
		if err != nil {
			return nil, err
		}
		y, err := base64urlDecode(k.Y)
		if err != nil {
			return nil, err
		}
		var curve elliptic.Curve
		switch k.Crv {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		case "P-521":
			curve = elliptic.P521()
		default:
			return nil, fmt.Errorf("unsupported curve: %s", k.Crv)
		}
		return &ecdsa.PublicKey{Curve: curve, X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}, nil
	default:
		return nil, fmt.Errorf("unsupported key type: %s", k.Kty)
	}
}

func base64urlDecode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

func (a *AuthMiddleware) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result := a.authenticate(r)
		if !result.OK {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      nil,
				Error:   NewError(-32600, "Unauthorized"),
			})
			return
		}
		r = r.WithContext(contextWithAgentID(r.Context(), result.AgentID))
		next.ServeHTTP(w, r)
	})
}

func (a *AuthMiddleware) CheckAuth(r *http.Request) (string, error) {
	// API key check
	if a.APIKey != "" {
		if r.Header.Get("X-Api-Key") == a.APIKey {
			return "", nil
		}
	}

	// Bearer check
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		token := strings.TrimPrefix(auth, "Bearer ")
		if a.BearerValidatorURL != "" {
			res := a.validateExternalBearer(token)
			if res.OK {
				return res.AgentID, nil
			}
			return "", fmt.Errorf("invalid bearer token")
		}
		if a.BearerJWKSURL != "" {
			res := a.validateJWTBearer(token)
			if res.OK {
				return res.AgentID, nil
			}
			return "", fmt.Errorf("invalid jwt token")
		}
		return "", fmt.Errorf("bearer auth not configured")
	}

	if a.APIKey != "" {
		return "", fmt.Errorf("invalid api key")
	}
	return "", fmt.Errorf("missing authorization")
}

func (a *AuthMiddleware) authenticate(r *http.Request) AuthResult {
	agentID, err := a.CheckAuth(r)
	if err != nil {
		return AuthResult{OK: false}
	}
	return AuthResult{OK: true, AgentID: agentID}
}

func (a *AuthMiddleware) validateExternalBearer(token string) AuthResult {
	body, _ := json.Marshal(map[string]string{"token": token})
	resp, err := http.Post(a.BearerValidatorURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return AuthResult{OK: false}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return AuthResult{OK: false}
	}

	var result struct {
		Valid   bool   `json:"valid"`
		AgentID string `json:"agent_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return AuthResult{OK: false}
	}
	return AuthResult{OK: result.Valid, AgentID: result.AgentID}
}

func (a *AuthMiddleware) validateJWTBearer(token string) AuthResult {
	if a.jwks == nil {
		a.jwks = newJWKSCache(a.BearerJWKSURL)
	}

	parsed, err := jwt.Parse(token, func(t *jwt.Token) (any, error) {
		kid, ok := t.Header["kid"].(string)
		if !ok {
			return nil, fmt.Errorf("missing kid header")
		}
		return a.jwks.getKey(kid)
	}, jwt.WithValidMethods([]string{"RS256", "RS384", "RS512", "ES256", "ES384", "ES512"}))
	if err != nil || !parsed.Valid {
		return AuthResult{OK: false}
	}

	sub, _ := parsed.Claims.GetSubject()
	return AuthResult{OK: true, AgentID: sub}
}

func contextWithAgentID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, agentIDKey{}, id)
}

func AgentIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(agentIDKey{}).(string)
	return v
}

type agentIDKey struct{}
