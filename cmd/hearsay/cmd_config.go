package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/gstranger/hearsay/internal/managed"
	"github.com/gstranger/hearsay/internal/postgres"
	"github.com/gstranger/hearsay/internal/sqlite"
	"github.com/gstranger/hearsay/pkg/hearsay"
)

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