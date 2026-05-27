package hearsay

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Version     int            `toml:"version"`
	Namespace   string         `toml:"namespace"`
	Provider    string         `toml:"provider"`
	ProviderCfg ProviderConfig `toml:"provider_config"`
	Defaults    DefaultsConfig   `toml:"defaults"`
	A2A         *A2AConfig     `toml:"a2a"`
}

type ProviderConfig struct {
	SQLite     *SQLiteConfig     `toml:"sqlite"`
	PostgreSQL *PostgreSQLConfig `toml:"postgresql"`
	DynamoDB   *DynamoDBConfig   `toml:"dynamodb"`
	D1         *D1Config         `toml:"d1"`
	Managed    *ManagedConfig    `toml:"managed"`
}

type SQLiteConfig struct {
	Path string `toml:"path"`
}

type PostgreSQLConfig struct {
	URL string `toml:"url"`
}

type DynamoDBConfig struct {
	Table string `toml:"table"`
}

type D1Config struct {
	DatabaseID string `toml:"database_id"`
}

type ManagedConfig struct {
	Endpoint string `toml:"endpoint"`
	Token    string `toml:"token"`
}

type DefaultsConfig struct {
	TTLSeconds    int  `toml:"ttl_seconds"`
	ClaimOnRead   bool `toml:"claim_on_read"`
	AutoHeartbeat bool `toml:"auto_heartbeat"`
	Locking       bool `toml:"locking"`
}

// A2AConfig configures the A2A JSON-RPC server.
type A2AConfig struct {
	Addr               string `toml:"addr"`
	APIKey             string `toml:"api_key"`
	BearerValidatorURL string `toml:"bearer_validator_url"`
	BearerJWKSURL      string `toml:"bearer_jwks_url"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}

func (c *Config) Save(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(c)
}
