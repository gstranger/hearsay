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
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/gstranger/hearsay/internal/a2a"
	"github.com/gstranger/hearsay/internal/managed"
	"github.com/gstranger/hearsay/internal/postgres"
	"github.com/gstranger/hearsay/internal/server"
	"github.com/gstranger/hearsay/internal/sqlite"
	"github.com/gstranger/hearsay/internal/watcher"
	"github.com/gstranger/hearsay/pkg/hearsay"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: hearsay <command> [args]")
		fmt.Fprintln(os.Stderr, "Commands: init, claim, release, heartbeat, query, check, namespace, serve, cursor, watch, status")
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "init":
		cmdInit(os.Args[2:])
	case "claim":
		cmdClaim(os.Args[2:])
	case "release":
		cmdRelease(os.Args[2:])
	case "heartbeat":
		cmdHeartbeat(os.Args[2:])
	case "query":
		cmdQuery(os.Args[2:])
	case "check":
		cmdCheck(os.Args[2:])
	case "namespace":
		cmdNamespace(os.Args[2:])
	case "serve":
		cmdServe(os.Args[2:])
	case "watch":
		cmdWatch(os.Args[2:])
	case "cursor":
		cmdCursor(os.Args[2:])
	case "status":
		cmdStatus(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		os.Exit(1)
	}
}

func loadClient() (*hearsay.Client, error) {
	provider, err := loadProvider()
	if err != nil {
		return nil, err
	}
	return loadClientFromProvider(provider)
}

func loadProvider() (hearsay.Provider, error) {
	cfg, err := hearsay.LoadConfig(".hearsay.toml")
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	switch cfg.Provider {
	case "sqlite":
		path := ".hearsay.db"
		if cfg.ProviderCfg.SQLite != nil && cfg.ProviderCfg.SQLite.Path != "" {
			path = cfg.ProviderCfg.SQLite.Path
		}
		p, err := sqlite.New(path)
		if err != nil {
			return nil, err
		}
		return p, nil
	case "postgresql":
		url := ""
		if cfg.ProviderCfg.PostgreSQL != nil && cfg.ProviderCfg.PostgreSQL.URL != "" {
			url = cfg.ProviderCfg.PostgreSQL.URL
		}
		p, err := postgres.New(url)
		if err != nil {
			return nil, err
		}
		return p, nil
	case "managed":
		endpoint := ""
		if cfg.ProviderCfg.Managed != nil && cfg.ProviderCfg.Managed.Endpoint != "" {
			endpoint = cfg.ProviderCfg.Managed.Endpoint
		}
		token := ""
		if cfg.ProviderCfg.Managed != nil && cfg.ProviderCfg.Managed.Token != "" {
			token = cfg.ProviderCfg.Managed.Token
		}
		return managed.New(endpoint, token), nil
	default:
		return nil, fmt.Errorf("unsupported provider: %s", cfg.Provider)
	}
}

func loadClientFromProvider(provider hearsay.Provider) (*hearsay.Client, error) {
	cfg, err := hearsay.LoadConfig(".hearsay.toml")
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	ctx := context.Background()
	if err := provider.CreateNamespace(ctx, hearsay.Namespace{ID: cfg.Namespace, CreatedAt: time.Now()}); err != nil {
		// namespace may already exist
	}
	return hearsay.NewClient(provider, cfg.Namespace), nil
}

func cmdInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	provider := fs.String("provider", "sqlite", "provider type")
	path := fs.String("path", ".hearsay.db", "sqlite path")
	pgURL := fs.String("postgresql-url", "", "postgresql connection URL")
	endpoint := fs.String("endpoint", "", "managed provider endpoint URL")
	token := fs.String("token", "", "managed provider auth token")
	namespace := fs.String("namespace", "default", "namespace ID")
	fs.Parse(args)

	locking := fs.Bool("locking", false, "enable exclusive locking mode (claims are rejected if resource is already claimed)")
	fs.Parse(args)

	cfg := &hearsay.Config{
		Version:   1,
		Namespace: *namespace,
		Provider:  *provider,
		Defaults:  hearsay.DefaultsConfig{TTLSeconds: 300, AutoHeartbeat: true, Locking: *locking},
	}
	switch *provider {
	case "sqlite":
		cfg.ProviderCfg.SQLite = &hearsay.SQLiteConfig{Path: *path}
	case "postgresql":
		cfg.ProviderCfg.PostgreSQL = &hearsay.PostgreSQLConfig{URL: *pgURL}
	case "managed":
		cfg.ProviderCfg.Managed = &hearsay.ManagedConfig{Endpoint: *endpoint, Token: *token}
	}
	if err := cfg.Save(".hearsay.toml"); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Created .hearsay.toml")
}

func cmdClaim(args []string) {
	fs := flag.NewFlagSet("claim", flag.ExitOnError)
	resource := fs.String("resource", "", "resource URI")
	op := fs.String("operation", "write", "operation")
	intent := fs.String("intent", "", "intent string")
	agentID := fs.String("agent", os.Getenv("HEARSAY_AGENT_ID"), "agent ID")
	fs.Parse(args)

	if *resource == "" {
		fmt.Fprintln(os.Stderr, "Usage: hearsay claim --resource <uri> --operation <op> --intent <intent>")
		os.Exit(1)
	}

	client, err := loadClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	resp, err := client.Claim(context.Background(), hearsay.ClaimRequest{
		ResourceURI: *resource,
		AgentID:     *agentID,
		Operation:   hearsay.Operation(*op),
		Intent:      *intent,
	})
	if err != nil {
		if ce, ok := err.(*hearsay.ConflictError); ok {
			data, _ := json.MarshalIndent(ce.Report, "", "  ")
			fmt.Fprintf(os.Stderr, "Conflict:\n%s\n", data)
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Claimed: %s\n", resp.ClaimID)
}

func cmdRelease(args []string) {
	fs := flag.NewFlagSet("release", flag.ExitOnError)
	claimID := fs.String("claim", "", "claim ID")
	outcome := fs.String("outcome", "succeeded", "outcome")
	fs.Parse(args)

	if *claimID == "" {
		fmt.Fprintln(os.Stderr, "Usage: hearsay release --claim <id>")
		os.Exit(1)
	}

	client, err := loadClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := client.Release(context.Background(), *claimID, hearsay.Outcome(*outcome)); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Released")
}

func cmdHeartbeat(args []string) {
	fs := flag.NewFlagSet("heartbeat", flag.ExitOnError)
	claimID := fs.String("claim", "", "claim ID")
	fs.Parse(args)

	if *claimID == "" {
		fmt.Fprintln(os.Stderr, "Usage: hearsay heartbeat --claim <id>")
		os.Exit(1)
	}

	client, err := loadClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := client.Heartbeat(context.Background(), *claimID); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Heartbeat sent")
}

func cmdQuery(args []string) {
	fs := flag.NewFlagSet("query", flag.ExitOnError)
	resource := fs.String("resource", "", "resource pattern")
	fs.Parse(args)

	client, err := loadClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	claims, err := client.ActiveClaims(context.Background(), *resource)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	data, _ := json.MarshalIndent(claims, "", "  ")
	fmt.Println(string(data))
}

func cmdCheck(args []string) {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	resource := fs.String("resource", "", "resource URI")
	op := fs.String("operation", "write", "operation")
	fs.Parse(args)

	if *resource == "" {
		fmt.Fprintln(os.Stderr, "Usage: hearsay check --resource <uri> --operation <op>")
		os.Exit(1)
	}

	client, err := loadClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	report, err := client.CheckConflict(context.Background(), *resource, hearsay.Operation(*op))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	data, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(data))
}

func cmdNamespace(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: hearsay namespace <create|delete|list>")
		os.Exit(1)
	}
	sub := args[0]
	switch sub {
	case "create":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: hearsay namespace create <id>")
			os.Exit(1)
		}
		fmt.Printf("Namespace %s created (config-only; namespace is created on first claim)\n", args[1])
	case "delete":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: hearsay namespace delete <id>")
			os.Exit(1)
		}
		fmt.Printf("Namespace %s deleted (config-only)\n", args[1])
	case "list":
		fmt.Println("[default]")
	default:
		fmt.Fprintf(os.Stderr, "Unknown namespace subcommand: %s\n", sub)
		os.Exit(1)
	}
}

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

	srv := server.New(client, provider, cfg.Defaults.Locking, *authToken, logFmt)

	httpSrv := &http.Server{Addr: *addr, Handler: srv}

	// Background sweeper: clean up expired claims every 60 seconds
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				before := time.Now().UTC()
				if err := provider.ReleaseExpired(context.Background(), cfg.Namespace, before); err != nil {
					log.Printf("sweeper error: %v", err)
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

func cmdWatch(args []string) {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	path := fs.String("path", "", "directory path to watch")
	namespace := fs.String("namespace", "default", "namespace ID")
	claimTTL := fs.Int("claim-ttl", 60, "retroactive claim TTL in seconds")
	fs.Parse(args)

	if *path == "" {
		fmt.Fprintln(os.Stderr, "Usage: hearsay watch --path <dir> --namespace <id> [--claim-ttl <seconds>]")
		os.Exit(1)
	}

	client, err := loadClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading client: %v\n", err)
		os.Exit(1)
	}

	w := watcher.New(watcher.Config{
		Path:      *path,
		Namespace: *namespace,
		Client:    client,
		ClaimTTL:  *claimTTL,
	})

	fmt.Printf("Watching %s (namespace: %s, claim TTL: %ds)\n", *path, *namespace, *claimTTL)
	if err := w.Run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "Watcher error: %v\n", err)
		os.Exit(1)
	}
}

func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	verbose := fs.Bool("verbose", false, "Show detailed claim and mailbox activity")
	fs.Parse(args)

	provider, err := loadProvider()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	cfg, err := hearsay.LoadConfig(".hearsay.toml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	claims, err := provider.ActiveClaims(ctx, cfg.Namespace, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error querying claims: %v\n", err)
		os.Exit(1)
	}

	agents := make(map[string]bool)
	for _, c := range claims {
		agents[c.AgentID] = true
	}

	var allMailboxMsgs []hearsay.MailboxMessage
	totalMailbox := 0
	unreadMailbox := 0
	for agentID := range agents {
		msgs, err := provider.GetMailbox(ctx, cfg.Namespace, agentID, hearsay.MailboxQueryOpts{Limit: 50})
		if err != nil {
			continue
		}
		totalMailbox += len(msgs)
		for _, m := range msgs {
			if !m.Read {
				unreadMailbox++
			}
		}
		allMailboxMsgs = append(allMailboxMsgs, msgs...)
	}

	providerInfo := cfg.Provider
	if cfg.Provider == "sqlite" {
		path := ".hearsay.db"
		if cfg.ProviderCfg.SQLite != nil && cfg.ProviderCfg.SQLite.Path != "" {
			path = cfg.ProviderCfg.SQLite.Path
		}
		providerInfo = fmt.Sprintf("sqlite (%s)", path)
	}

	fmt.Printf("Namespace:       %s\n", cfg.Namespace)
	fmt.Printf("Provider:        %s\n", providerInfo)
	fmt.Printf("Active claims:   %d\n", len(claims))
	fmt.Printf("Mailbox:         %d messages (%d unread)\n", totalMailbox, unreadMailbox)

	agentList := make([]string, 0, len(agents))
	for a := range agents {
		agentList = append(agentList, a)
	}
	sort.Strings(agentList)
	fmt.Printf("Agents online:   %d", len(agents))
	if len(agentList) > 0 {
		fmt.Printf(" (%s)", strings.Join(agentList, ", "))
	}
	fmt.Println()

	if *verbose {
		fmt.Println()
		if len(claims) > 0 {
			fmt.Println("Active claims:")
			for _, c := range claims {
				age := time.Since(c.CreatedAt).Truncate(time.Second)
				fmt.Printf("  %-10s %-30s %-8s %-15s %s ago\n",
					c.AgentID, c.ResourceURI, c.Operation, c.Intent, age)
			}
			fmt.Println()
		}

		if len(allMailboxMsgs) > 0 {
			fmt.Println("Recent mailbox (last 10):")
			sort.Slice(allMailboxMsgs, func(i, j int) bool {
				return allMailboxMsgs[i].CreatedAt.After(allMailboxMsgs[j].CreatedAt)
			})
			limit := 10
			if len(allMailboxMsgs) < limit {
				limit = len(allMailboxMsgs)
			}
			for _, m := range allMailboxMsgs[:limit] {
				status := " "
				if m.Read {
					status = "✓"
				}
				age := time.Since(m.CreatedAt).Truncate(time.Second)
				fmt.Printf("  %s %-10s → %-10s %-20s %s ago\n",
					status, m.From, m.To, string(m.Type), age)
			}
		}
	}
}
