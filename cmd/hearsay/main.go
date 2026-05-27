package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
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
		fmt.Fprintln(os.Stderr, "Commands: init, claim, release, heartbeat, query, check, namespace, serve, cursor, watch")
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

	// Start A2A server if configured
	if a2aCfg != nil {
		authMW := &a2a.AuthMiddleware{
			APIKey:             a2aCfg.APIKey,
			BearerValidatorURL: a2aCfg.BearerValidatorURL,
			BearerJWKSURL:      a2aCfg.BearerJWKSURL,
		}
		a2aSrv := a2a.NewServer(a2aCfg, client, provider, authMW, cfg.Namespace)
		go func() {
			fmt.Printf("A2A server listening on %s\n", a2aCfg.Addr)
			if err := http.ListenAndServe(a2aCfg.Addr, authMW.Middleware(a2aSrv)); err != nil {
				fmt.Fprintf(os.Stderr, "A2A server error: %v\n", err)
			}
		}()
	}

	srv := server.New(client, provider, cfg.Defaults.Locking)
	fmt.Printf("Listening on %s\n", *addr)
	if err := http.ListenAndServe(*addr, srv); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
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
