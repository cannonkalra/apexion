// Package config loads Apexion configuration from a YAML file, environment
// variables, and sane defaults using viper.
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config is the root configuration object.
type Config struct {
	Server  ServerConfig  `mapstructure:"server"`
	Storage StorageConfig `mapstructure:"storage"`
	MinIO   MinIOConfig   `mapstructure:"minio"`
	Crawler CrawlerConfig `mapstructure:"crawler"`
	Catalog CatalogConfig `mapstructure:"catalog"`
	Log     LogConfig     `mapstructure:"log"`
}

// CatalogConfig tunes the logical SQL catalog.
type CatalogConfig struct {
	// VirtualPartitionPrefix names generated partition columns for bare-directory
	// datasets, e.g. "pt" → pt0, pt1. VirtualPartitionSeparator sits between the
	// prefix and index, e.g. "_" → pt_0, pt_1.
	VirtualPartitionPrefix    string          `mapstructure:"virtual_partition_prefix"`
	VirtualPartitionSeparator string          `mapstructure:"virtual_partition_separator"`
	Discovery                 DiscoveryConfig `mapstructure:"discovery"`
}

// DiscoveryConfig selects how the crawler groups objects into datasets.
type DiscoveryConfig struct {
	// Strategy is positional (default; bare dirs → pt0/pt1/…), hive (only
	// key=value dirs partition; others are per-directory datasets), or auto.
	Strategy string `mapstructure:"strategy"`
}

// ServerConfig configures the HTTP server.
type ServerConfig struct {
	Host            string        `mapstructure:"host"`
	Port            int           `mapstructure:"port"`
	ReadTimeout     time.Duration `mapstructure:"read_timeout"`
	WriteTimeout    time.Duration `mapstructure:"write_timeout"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
}

// StorageConfig configures the embedded DuckDB catalog store.
type StorageConfig struct {
	// Path is the DuckDB database file; ":memory:" for ephemeral.
	Path          string `mapstructure:"path"`
	MigrationsDir string `mapstructure:"migrations_dir"`
	MaxOpenConns  int    `mapstructure:"max_open_conns"`
}

// MinIOConfig holds optional object-store credentials used for headless/CLI
// runs and as the pre-connection fallback store. It is NOT persisted as a
// connection: the app starts with zero configured connections and you create
// your first one from the Settings page. On EC2 (or any host with an attached
// IAM role) these can be left blank and the AWS credential chain is used.
type MinIOConfig struct {
	Endpoint  string `mapstructure:"endpoint"`
	AccessKey string `mapstructure:"access_key"`
	SecretKey string `mapstructure:"secret_key"`
	UseSSL    bool   `mapstructure:"use_ssl"`
	Region    string `mapstructure:"region"`
}

// CrawlerConfig tunes the crawler.
type CrawlerConfig struct {
	Workers         int           `mapstructure:"workers"`
	RateLimit       int           `mapstructure:"rate_limit"` // ops/sec, 0 = unlimited
	ListPageSize    int           `mapstructure:"list_page_size"`
	SampleBytes     int64         `mapstructure:"sample_bytes"`     // max bytes read per file for detection
	CheckpointEvery int           `mapstructure:"checkpoint_every"` // objects between checkpoints
	IgnoreHidden    bool          `mapstructure:"ignore_hidden"`
	Timeout         time.Duration `mapstructure:"timeout"`
}

// LogConfig configures logging.
type LogConfig struct {
	Level  string `mapstructure:"level"`
	Pretty bool   `mapstructure:"pretty"`
}

// Addr returns host:port for the server.
func (s ServerConfig) Addr() string { return fmt.Sprintf("%s:%d", s.Host, s.Port) }

// Load reads configuration. path may be empty to rely on defaults + env.
// Environment variables use the APEXION_ prefix with underscores replacing
// dots, e.g. APEXION_MINIO_ENDPOINT.
func Load(path string) (*Config, error) {
	v := viper.New()
	setDefaults(v)

	v.SetEnvPrefix("APEXION")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if path != "" {
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("read config %q: %w", path, err)
		}
	}

	var c Config
	if err := v.Unmarshal(&c); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	return &c, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.port", 8080)
	v.SetDefault("server.read_timeout", "30s")
	v.SetDefault("server.write_timeout", "60s")
	v.SetDefault("server.shutdown_timeout", "15s")

	v.SetDefault("storage.path", "apexion.duckdb")
	v.SetDefault("storage.migrations_dir", "migrations")
	v.SetDefault("storage.max_open_conns", 1)

	v.SetDefault("minio.endpoint", "localhost:9000")
	v.SetDefault("minio.access_key", "minioadmin")
	v.SetDefault("minio.secret_key", "minioadmin")
	v.SetDefault("minio.use_ssl", false)
	v.SetDefault("minio.region", "us-east-1")

	v.SetDefault("crawler.workers", 8)
	v.SetDefault("crawler.rate_limit", 0)
	v.SetDefault("crawler.list_page_size", 1000)
	v.SetDefault("crawler.sample_bytes", 262144)
	v.SetDefault("crawler.checkpoint_every", 500)
	v.SetDefault("crawler.ignore_hidden", true)
	v.SetDefault("crawler.timeout", "1h")

	v.SetDefault("catalog.virtual_partition_prefix", "pt")
	v.SetDefault("catalog.virtual_partition_separator", "")
	v.SetDefault("catalog.discovery.strategy", "positional")

	v.SetDefault("log.level", "info")
	v.SetDefault("log.pretty", true)
}
