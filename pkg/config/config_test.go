package config

import (
	"os"
	"testing"
	"time"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "*.conf")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	f.Close()
	return f.Name()
}

func TestLoadBasic(t *testing.T) {
	path := writeTemp(t, "cluster 127.0.0.1:7000\nport 8888\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 8888 {
		t.Errorf("port: want 8888, got %d", cfg.Port)
	}
	if len(cfg.EntryPoints) != 1 || cfg.EntryPoints[0] != "127.0.0.1:7000" {
		t.Errorf("entry points: want [127.0.0.1:7000], got %v", cfg.EntryPoints)
	}
}

func TestLoadMultipleEntryPoints(t *testing.T) {
	path := writeTemp(t, "cluster 127.0.0.1:7000\ncluster 127.0.0.1:7001\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.EntryPoints) != 2 {
		t.Errorf("want 2 entry points, got %d", len(cfg.EntryPoints))
	}
}

func TestLoadMissingClusterReturnsError(t *testing.T) {
	path := writeTemp(t, "port 7777\n")
	_, err := Load(path)
	if err == nil {
		t.Error("expected error when no cluster entry points configured")
	}
}

func TestLoadIgnoresCommentsAndBlankLines(t *testing.T) {
	path := writeTemp(t, "# comment\n\ncluster 127.0.0.1:7000\n  # another comment\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.EntryPoints) != 1 {
		t.Errorf("want 1 entry point, got %d", len(cfg.EntryPoints))
	}
}

func TestLoadAuth(t *testing.T) {
	path := writeTemp(t, "cluster 127.0.0.1:7000\nusername alice\npassword secret\nclient-password clientpw\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Username != "alice" {
		t.Errorf("username: want alice, got %q", cfg.Username)
	}
	if cfg.Password != "secret" {
		t.Errorf("password: want secret, got %q", cfg.Password)
	}
	if cfg.ClientPassword != "clientpw" {
		t.Errorf("client-password: want clientpw, got %q", cfg.ClientPassword)
	}
}

func TestLoadDurations(t *testing.T) {
	path := writeTemp(t, "cluster 127.0.0.1:7000\nidle-timeout 60s\nrefresh-interval 10s\nread-timeout 5s\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.IdleTimeout != 60*time.Second {
		t.Errorf("idle-timeout: want 60s, got %v", cfg.IdleTimeout)
	}
	if cfg.RefreshInterval != 10*time.Second {
		t.Errorf("refresh-interval: want 10s, got %v", cfg.RefreshInterval)
	}
	if cfg.ReadTimeout != 5*time.Second {
		t.Errorf("read-timeout: want 5s, got %v", cfg.ReadTimeout)
	}
}

func TestLoadReadMode(t *testing.T) {
	cases := []struct {
		input string
		want  ReadMode
	}{
		{"master-only", ReadModeMasterOnly},
		{"master-slave", ReadModeMasterSlave},
		{"slave-only", ReadModeSlaveOnly},
	}
	for _, c := range cases {
		path := writeTemp(t, "cluster 127.0.0.1:7000\nread-mode "+c.input+"\n")
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load(%q): %v", c.input, err)
		}
		if cfg.ReadMode != c.want {
			t.Errorf("read-mode %q: want %v, got %v", c.input, c.want, cfg.ReadMode)
		}
	}
}

func TestLoadInvalidPort(t *testing.T) {
	path := writeTemp(t, "cluster 127.0.0.1:7000\nport notanumber\n")
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for invalid port")
	}
}

func TestLoadMaxClients(t *testing.T) {
	path := writeTemp(t, "cluster 127.0.0.1:7000\nmax-clients 500\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MaxClients != 500 {
		t.Errorf("max-clients: want 500, got %d", cfg.MaxClients)
	}
}

func TestLoadPoolSize(t *testing.T) {
	path := writeTemp(t, "cluster 127.0.0.1:7000\npool-size 50\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PoolSize != 50 {
		t.Errorf("pool-size: want 50, got %d", cfg.PoolSize)
	}
}

func TestDefaultValues(t *testing.T) {
	cfg := Default()
	if cfg.Port != 7777 {
		t.Errorf("default port: want 7777, got %d", cfg.Port)
	}
	if cfg.PoolSize != 20 {
		t.Errorf("default pool-size: want 20, got %d", cfg.PoolSize)
	}
	if cfg.ReadMode != ReadModeMasterOnly {
		t.Errorf("default read-mode: want ReadModeMasterOnly, got %v", cfg.ReadMode)
	}
	if cfg.MaxRedirects != 16 {
		t.Errorf("default max-redirects: want 16, got %d", cfg.MaxRedirects)
	}
}

func TestParseReadMode(t *testing.T) {
	if m, ok := ParseReadMode("unknown"); ok || m != ReadModeMasterOnly {
		t.Errorf("unknown mode: want (ReadModeMasterOnly, false), got (%v, %v)", m, ok)
	}
	if m, ok := ParseReadMode("slave-only"); !ok || m != ReadModeSlaveOnly {
		t.Errorf("slave-only: want (ReadModeSlaveOnly, true), got (%v, %v)", m, ok)
	}
}

func TestFromArgs(t *testing.T) {
	cfg := FromArgs([]string{"127.0.0.1:7000", "127.0.0.1:7001"}, WithPort(9999))
	if len(cfg.EntryPoints) != 2 {
		t.Errorf("want 2 entry points, got %d", len(cfg.EntryPoints))
	}
	if cfg.Port != 9999 {
		t.Errorf("want port 9999, got %d", cfg.Port)
	}
}

func TestConfigAddr(t *testing.T) {
	cfg := &Config{Bind: "0.0.0.0", Port: 7777}
	if got := cfg.Addr(); got != "0.0.0.0:7777" {
		t.Errorf("Addr(): want 0.0.0.0:7777, got %q", got)
	}
}
