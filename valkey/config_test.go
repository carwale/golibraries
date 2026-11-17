package valkey

import (
	"strings"
	"testing"
	"time"
)

func iamConfig() Config {
	return Config{
		Host:        "master.my-cache.aps1.cache.amazonaws.com",
		IsCluster:   Bool(false),
		Username:    "my-iam-user",
		ClusterName: "My-Cache",
		Region:      "ap-south-1",
	}
}

func TestConfigValidateDefaults(t *testing.T) {
	cfg := iamConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if cfg.Port != 6379 {
		t.Errorf("Port = %d, want 6379", cfg.Port)
	}
	if cfg.TLS == nil || !*cfg.TLS {
		t.Error("TLS should default to true")
	}
	if cfg.AuthMode != AuthIAM {
		t.Errorf("AuthMode = %q, want iam", cfg.AuthMode)
	}
	if cfg.ClusterName != "my-cache" {
		t.Errorf("ClusterName = %q, want lowercased my-cache", cfg.ClusterName)
	}
	if cfg.RequestTimeout != 250*time.Millisecond {
		t.Errorf("RequestTimeout = %v", cfg.RequestTimeout)
	}
	if cfg.RefreshCacheDuration != 10*time.Second {
		t.Errorf("RefreshCacheDuration = %v", cfg.RefreshCacheDuration)
	}
	if cfg.ShardedPubSub == nil || *cfg.ShardedPubSub {
		t.Error("ShardedPubSub should default to IsCluster (false)")
	}

	cluster := iamConfig()
	cluster.IsCluster = Bool(true)
	if err := cluster.Validate(); err != nil {
		t.Fatalf("Validate cluster: %v", err)
	}
	if !*cluster.ShardedPubSub {
		t.Error("ShardedPubSub should default to true in cluster mode")
	}
}

func TestConfigValidateErrors(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"missing host", func(c *Config) { c.Host = " " }, "Host"},
		{"missing IsCluster", func(c *Config) { c.IsCluster = nil }, "IsCluster"},
		{"bad port", func(c *Config) { c.Port = 70000 }, "Port"},
		{"iam without tls", func(c *Config) { c.TLS = Bool(false) }, "TLS"},
		{"iam without username", func(c *Config) { c.Username = "" }, "Username"},
		{"iam without cluster name", func(c *Config) { c.ClusterName = "" }, "ClusterName"},
		{"iam without region", func(c *Config) { c.Region = "" }, "Region"},
		{"password without password", func(c *Config) { c.AuthMode = AuthPassword }, "Password"},
		{"unknown auth mode", func(c *Config) { c.AuthMode = "kerberos" }, "AuthMode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := iamConfig()
			tt.mutate(&cfg)
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() = %v, want error mentioning %q", err, tt.want)
			}
		})
	}
}

func TestConfigValidateLocalModes(t *testing.T) {
	none := Config{Host: "localhost", IsCluster: Bool(false), TLS: Bool(false), AuthMode: AuthNone}
	if err := none.Validate(); err != nil {
		t.Fatalf("AuthNone without TLS: %v", err)
	}
	pw := Config{Host: "localhost", IsCluster: Bool(false), TLS: Bool(false), AuthMode: AuthPassword, Password: "secret"}
	if err := pw.Validate(); err != nil {
		t.Fatalf("AuthPassword: %v", err)
	}
	var nilCfg *Config
	if err := nilCfg.Validate(); err == nil {
		t.Fatal("nil config should fail")
	}
}
