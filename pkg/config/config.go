package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type ReadMode int

const (
	ReadModeMasterOnly  ReadMode = iota
	ReadModeMasterSlave          // balance reads across master and slaves
	ReadModeSlaveOnly            // prefer slave, fall back to master if no slave
)

func ParseReadMode(s string) (ReadMode, bool) {
	switch s {
	case "master-only":
		return ReadModeMasterOnly, true
	case "master-slave":
		return ReadModeMasterSlave, true
	case "slave-only":
		return ReadModeSlaveOnly, true
	default:
		return ReadModeMasterOnly, false
	}
}

type Config struct {
	Bind            string
	Port            int
	EntryPoints     []string
	Username        string
	Password        string
	ClientPassword  string
	PoolSize        int
	Workers         int
	IdleTimeout     time.Duration
	ReadTimeout     time.Duration
	RefreshInterval time.Duration
	LogLevel        string
	MaxRedirects    int
	ReadMode        ReadMode
	MaxClients      int
}

func Default() *Config {
	return &Config{
		Bind:            "0.0.0.0",
		Port:            7777,
		PoolSize:        20,
		IdleTimeout:     30 * time.Second,
		ReadTimeout:     10 * time.Second,
		RefreshInterval: 5 * time.Second,
		LogLevel:        "info",
		MaxRedirects:    16,
		ReadMode:        ReadModeMasterOnly,
	}
}

func (c *Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.Bind, c.Port)
}

func Load(path string) (*Config, error) {
	cfg := Default()
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		val := strings.TrimSpace(parts[1])

		switch key {
		case "bind":
			cfg.Bind = val
		case "port":
			n, err := strconv.Atoi(val)
			if err != nil {
				return nil, fmt.Errorf("invalid port %q: %w", val, err)
			}
			cfg.Port = n
		case "cluster":
			cfg.EntryPoints = append(cfg.EntryPoints, val)
		case "username":
			cfg.Username = val
		case "password", "auth":
			cfg.Password = val
		case "auth-user":
			cfg.Username = val
		case "pool-size", "connections-pool-size":
			n, err := strconv.Atoi(val)
			if err != nil {
				return nil, fmt.Errorf("invalid pool-size %q: %w", val, err)
			}
			cfg.PoolSize = n
		case "idle-timeout":
			d, err := time.ParseDuration(val)
			if err != nil {
				return nil, fmt.Errorf("invalid idle-timeout %q: %w", val, err)
			}
			cfg.IdleTimeout = d
		case "refresh-interval":
			d, err := time.ParseDuration(val)
			if err != nil {
				return nil, fmt.Errorf("invalid refresh-interval %q: %w", val, err)
			}
			cfg.RefreshInterval = d
		case "log-level":
			cfg.LogLevel = val
		case "max-redirects":
			n, err := strconv.Atoi(val)
			if err != nil {
				return nil, fmt.Errorf("invalid max-redirects %q: %w", val, err)
			}
			cfg.MaxRedirects = n
		case "read-timeout":
			d, err := time.ParseDuration(val)
			if err != nil {
				return nil, fmt.Errorf("invalid read-timeout %q: %w", val, err)
			}
			cfg.ReadTimeout = d
		case "client-password":
			cfg.ClientPassword = val
		case "read-replica":
			if val == "yes" || val == "1" || val == "true" {
				cfg.ReadMode = ReadModeMasterSlave
			}
		case "read-mode":
			if m, ok := ParseReadMode(val); ok {
				cfg.ReadMode = m
			}
		case "max-clients":
			n, err := strconv.Atoi(val)
			if err != nil {
				return nil, fmt.Errorf("invalid max-clients %q: %w", val, err)
			}
			cfg.MaxClients = n
		case "workers":
			n, err := strconv.Atoi(val)
			if err != nil {
				return nil, fmt.Errorf("invalid workers %q: %w", val, err)
			}
			cfg.Workers = n
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if len(cfg.EntryPoints) == 0 {
		return nil, fmt.Errorf("no cluster entry points configured")
	}

	return cfg, nil
}

func FromArgs(entryPoints []string, opts ...func(*Config)) *Config {
	cfg := Default()
	cfg.EntryPoints = entryPoints
	for _, o := range opts {
		o(cfg)
	}
	return cfg
}

func WithPort(port int) func(*Config) {
	return func(c *Config) { c.Port = port }
}

func WithPassword(username, password string) func(*Config) {
	return func(c *Config) {
		c.Username = username
		c.Password = password
	}
}

func WithClientPassword(password string) func(*Config) {
	return func(c *Config) { c.ClientPassword = password }
}

func WithPoolSize(n int) func(*Config) {
	return func(c *Config) { c.PoolSize = n }
}

func WithReadMode(m ReadMode) func(*Config) {
	return func(c *Config) { c.ReadMode = m }
}
