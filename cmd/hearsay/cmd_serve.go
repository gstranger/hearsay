//go:build !wasm

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gstranger/hearsay/internal/a2a"
	"github.com/gstranger/hearsay/internal/managed"
	"github.com/gstranger/hearsay/internal/postgres"
	"github.com/gstranger/hearsay/internal/server"
	"github.com/gstranger/hearsay/internal/sqlite"
	"github.com/gstranger/hearsay/pkg/hearsay"
)

func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "localhost:8080", "listen address")
	a2aAddr := fs.String("a2a-addr", "", "A2A server listen address (e.g. localhost:8081). Omit to disable A2A.")
	a2aAPIKey := fs.String("a2a-api-key", "", "A2A API key for api-key auth scheme")
	a2aBearerValidatorURL := fs.String("a2a-bearer-validator-url", "", "External URL for Bearer token validation")
	a2aBearerJWKSURL := fs.String("a2a-bearer-jwks-url", "", "JWKS URL for built-in JWT Bearer validation")
	authToken := fs.String("auth-token", "", "Auth token for REST API. When set, mutating endpoints require Authorization: Bearer <token> or X-Api-Key: <token>")
	logFormat := fs.String("log-format", "text", "Log format: text or json")
	tlsCert := fs.String("tls-cert", "", "Path to TLS certificate file")
	tlsKey := fs.String("tls-key", "", "Path to TLS private key file")
	tlsAuto := fs.Bool("tls-auto", false, "Auto-generate self-signed TLS certificate")
	rateLimit := fs.Int("rate-limit", 0, "Max requests per second per agent (0 = unlimited)")
	rateBurst := fs.Int("rate-burst", 10, "Max burst size per agent")
	agentTimeout := fs.Int("agent-timeout", 60, "Seconds without heartbeat before agent is declared dead (0 = disabled)")
	auditLevel := fs.String("audit-level", "off", "Audit log verbosity: off, coordination, security, full")
	fs.Parse(args)

	cfg, err := hearsay.LoadConfig(".hearsay.toml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	provider, err := loadProviderFromConfig(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	client, err := loadClientFromProvider(provider)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var logFmt server.LogFormat
	if *logFormat == "json" {
		logFmt = server.LogFormatJSON
	} else {
		logFmt = server.LogFormatText
	}

	// Build A2A config from flags OR toml
	var a2aCfg *hearsay.A2AConfig
	if *a2aAddr != "" {
		a2aCfg = &hearsay.A2AConfig{
			Addr:               *a2aAddr,
			APIKey:             *a2aAPIKey,
			BearerValidatorURL: *a2aBearerValidatorURL,
			BearerJWKSURL:      *a2aBearerJWKSURL,
		}
	} else if cfg.A2A != nil && cfg.A2A.Addr != "" {
		a2aCfg = cfg.A2A
	}

	// Auto-generate self-signed TLS certificate if --tls-auto set
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

	// Start A2A server if configured
	var a2aHttpSrv *http.Server
	if a2aCfg != nil {
		authMW := &a2a.AuthMiddleware{
			APIKey:             a2aCfg.APIKey,
			BearerValidatorURL: a2aCfg.BearerValidatorURL,
			BearerJWKSURL:      a2aCfg.BearerJWKSURL,
		}
		a2aSrv := a2a.NewServer(a2aCfg, client, provider, authMW, cfg.Namespace)
		a2aHttpSrv = &http.Server{Addr: a2aCfg.Addr, Handler: authMW.Middleware(a2aSrv)}
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
	}

	auditLvl := hearsay.ParseAuditLevel(*auditLevel)

	srv := server.New(client, provider, cfg.Defaults.Locking, *authToken, logFmt, *rateLimit, *rateBurst, auditLvl, cfg.Namespace)

	httpSrv := &http.Server{Addr: *addr, Handler: srv}

	// Background sweeper: clean up expired claims every 60 seconds
	server.RunSweeper(context.Background(), provider, cfg.Namespace, 60*time.Second)

	// Background death detector: auto-release claims from dead agents
	go func() {
		if *agentTimeout <= 0 {
			return
		}
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				now := time.Now().UTC()
				claims, err := provider.ActiveClaims(context.Background(), cfg.Namespace, "")
				if err != nil {
					log.Printf("death detector error: %v", err)
					continue
				}

				agentClaims := make(map[string][]hearsay.Claim)
				for _, c := range claims {
					agentClaims[c.AgentID] = append(agentClaims[c.AgentID], c)
				}

				for agentID, claims := range agentClaims {
					state, err := provider.AgentState(context.Background(), cfg.Namespace, agentID)
					if err != nil || state.LastSeen.IsZero() {
						continue
					}
					if now.Sub(state.LastSeen) > time.Duration(*agentTimeout)*time.Second {
						log.Printf("agent %s appears dead (last seen %s ago), releasing %d claims",
							agentID, now.Sub(state.LastSeen).Truncate(time.Second), len(claims))
						for _, c := range claims {
							payload, _ := json.Marshal(map[string]string{
								"claim_id": c.ClaimID,
								"outcome":  "abandoned_by_death",
							})
							msg := hearsay.Message{
								Type:    hearsay.MsgRelease,
								AgentID: agentID,
								Payload: payload,
							}
							if err := provider.Append(context.Background(), cfg.Namespace, []hearsay.Message{msg}); err != nil {
								log.Printf("death detector: failed to release claim %s: %v", c.ClaimID, err)
							}
						}
					}
				}
			}
		}
	}()

	// Graceful shutdown on SIGINT/SIGTERM
	idleConnsClosed := make(chan struct{})
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		<-sigCh
		log.Println("shutting down...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown error: %v", err)
		}
		close(idleConnsClosed)
	}()

	// Shutdown A2A server when REST server shuts down
	if a2aHttpSrv != nil {
		go func() {
			<-idleConnsClosed
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			a2aHttpSrv.Shutdown(shutdownCtx)
		}()
	}

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
	<-idleConnsClosed
	log.Println("server stopped")
}

func loadProviderFromConfig(cfg *hearsay.Config) (hearsay.Provider, error) {
	switch cfg.Provider {
	case "sqlite":
		path := ".hearsay.db"
		if cfg.ProviderCfg.SQLite != nil && cfg.ProviderCfg.SQLite.Path != "" {
			path = cfg.ProviderCfg.SQLite.Path
		}
		return sqlite.New(path)
	case "postgresql":
		url := ""
		if cfg.ProviderCfg.PostgreSQL != nil {
			url = cfg.ProviderCfg.PostgreSQL.URL
		}
		return postgres.New(url)
	case "managed":
		endpoint := ""
		token := ""
		if cfg.ProviderCfg.Managed != nil {
			endpoint = cfg.ProviderCfg.Managed.Endpoint
			token = cfg.ProviderCfg.Managed.Token
		}
		return managed.New(endpoint, token), nil
	default:
		return nil, fmt.Errorf("unknown provider: %s", cfg.Provider)
	}
}