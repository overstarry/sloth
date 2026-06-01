// Package config loads sloth configuration from a YAML file with environment
// variable overrides for secrets.
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration for the sloth agent.
type Config struct {
	// Server holds the HTTP API server settings.
	Server ServerConfig `yaml:"server"`
	// Target is the PostgreSQL instance being monitored (read-only).
	Target DBConfig `yaml:"target"`
	// Store is the database sloth uses to persist its own state.
	Store DBConfig `yaml:"store"`
	// Collector controls how slow SQL is sampled from the target.
	Collector CollectorConfig `yaml:"collector"`
	// LLM configures the diagnosis model provider.
	LLM LLMConfig `yaml:"llm"`
	// Notify configures alert delivery channels and routing.
	Notify NotifyConfig `yaml:"notify"`
}

// ServerConfig configures the HTTP API server.
type ServerConfig struct {
	Addr string `yaml:"addr"`
}

// DBConfig is a PostgreSQL connection definition.
type DBConfig struct {
	DSN              string        `yaml:"dsn"`
	MaxConns         int32         `yaml:"max_conns"`
	StatementTimeout time.Duration `yaml:"statement_timeout"`
}

// CollectorConfig controls slow SQL sampling.
type CollectorConfig struct {
	// Interval between pg_stat_statements snapshots.
	Interval time.Duration `yaml:"interval"`
	// TopN slowest statements to persist per cycle.
	TopN int `yaml:"top_n"`
	// MinMeanExecMs filters out statements faster than this mean duration.
	MinMeanExecMs float64 `yaml:"min_mean_exec_ms"`
}

// LLMConfig configures the diagnosis model.
type LLMConfig struct {
	Provider    string  `yaml:"provider"` // claude | openai | mock
	Model       string  `yaml:"model"`
	APIKey      string  `yaml:"api_key"`
	BaseURL     string  `yaml:"base_url"`
	MaxTokens   int     `yaml:"max_tokens"`
	Temperature float32 `yaml:"temperature"`
}

// NotifyConfig configures alert delivery.
type NotifyConfig struct {
	Enabled  bool            `yaml:"enabled"`
	Rules    NotifyRules     `yaml:"rules"`
	Channels []ChannelConfig `yaml:"channels"`
	// Cooldown is the per-fingerprint quiet window to avoid alert storms.
	Cooldown time.Duration `yaml:"cooldown"`
}

// NotifyRules routes alert levels to channels.
type NotifyRules struct {
	Critical RuleConfig `yaml:"critical"`
	Warn     RuleConfig `yaml:"warn"`
}

// RuleConfig is a single routing rule.
type RuleConfig struct {
	Channels []string `yaml:"channels"`
	Mention  []string `yaml:"mention"`
}

// ChannelConfig defines one notification channel.
type ChannelConfig struct {
	Name       string `yaml:"name"`
	Type       string `yaml:"type"` // wecom | feishu | lark
	Webhook    string `yaml:"webhook"`
	SignSecret string `yaml:"sign_secret"`
}

// Load reads configuration from path, applies defaults, and overlays
// environment overrides for secrets (SLOTH_LLM_API_KEY, SLOTH_TARGET_DSN, ...).
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	c.applyDefaults()
	c.applyEnv()
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.Server.Addr == "" {
		c.Server.Addr = ":8080"
	}
	if c.Collector.Interval == 0 {
		c.Collector.Interval = time.Minute
	}
	if c.Collector.TopN == 0 {
		c.Collector.TopN = 20
	}
	if c.LLM.Provider == "" {
		c.LLM.Provider = "mock"
	}
	if c.LLM.MaxTokens == 0 {
		c.LLM.MaxTokens = 2048
	}
	if c.Notify.Cooldown == 0 {
		c.Notify.Cooldown = 30 * time.Minute
	}
}

func (c *Config) applyEnv() {
	if v := os.Getenv("SLOTH_TARGET_DSN"); v != "" {
		c.Target.DSN = v
	}
	if v := os.Getenv("SLOTH_STORE_DSN"); v != "" {
		c.Store.DSN = v
	}
	if v := os.Getenv("SLOTH_LLM_API_KEY"); v != "" {
		c.LLM.APIKey = v
	}
}

func (c *Config) validate() error {
	if c.Target.DSN == "" {
		return fmt.Errorf("target.dsn is required")
	}
	if c.Store.DSN == "" {
		return fmt.Errorf("store.dsn is required")
	}
	return nil
}
