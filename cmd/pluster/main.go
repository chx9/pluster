package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/pluster/pluster/pkg/config"
	"github.com/pluster/pluster/pkg/server"
)

var (
	flagPort       = flag.Int("port", 7777, "Proxy listen port")
	flagBind       = flag.String("bind", "0.0.0.0", "Bind address")
	flagPassword   = flag.String("auth", "", "Backend cluster password")
	flagUsername   = flag.String("auth-user", "", "Backend cluster username (Redis 6+ ACL)")
	flagPoolSize   = flag.Int("pool-size", 10, "Connection pool size per node")
	flagConfig     = flag.String("c", "", "Config file path")
	flagLogLevel   = flag.String("log-level", "info", "Log level (debug/info/warn/error)")
	flagVersion    = flag.Bool("version", false, "Print version and exit")
	flagPprof      = flag.String("pprof", "", "Enable pprof HTTP server on this address (e.g. 127.0.0.1:6060)")
)

const version = "1.0.0"

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: pluster [options] cluster-addr [cluster-addr ...]\n\n")
		fmt.Fprintf(os.Stderr, "pluster is a Redis Cluster proxy.\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExample:\n")
		fmt.Fprintf(os.Stderr, "  pluster 127.0.0.1:7000\n")
		fmt.Fprintf(os.Stderr, "  pluster --port 7778 127.0.0.1:7000 127.0.0.1:7001\n")
	}
	flag.Parse()

	debug.SetGCPercent(200)

	if *flagVersion {
		fmt.Printf("pluster version %s\n", version)
		os.Exit(0)
	}

	var cfg *config.Config

	if *flagConfig != "" {
		var err error
		cfg, err = config.Load(*flagConfig)
		if err != nil {
			slog.Error("failed to load config", "err", err)
			os.Exit(1)
		}
		overrideFromFlags(cfg)
	} else {
		entryPoints := flag.Args()
		if len(entryPoints) == 0 {
			flag.Usage()
			os.Exit(1)
		}
		cfg = config.FromArgs(entryPoints,
			config.WithPort(*flagPort),
			config.WithPassword(*flagUsername, *flagPassword),
			config.WithPoolSize(*flagPoolSize),
		)
		cfg.Bind = *flagBind
		cfg.LogLevel = *flagLogLevel
	}

	initLogger(cfg.LogLevel)

	if *flagPprof != "" {
		go func() {
			slog.Info("pprof listening", "addr", *flagPprof)
			_ = http.ListenAndServe(*flagPprof, nil)
		}()
	}

	slog.Info("pluster starting", "version", version)
	slog.Info("cluster entry points", "addrs", strings.Join(cfg.EntryPoints, ", "))
	slog.Info("listen", "addr", cfg.Addr())


	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv, err := server.New(cfg)
	if err != nil {
		slog.Error("failed to create server", "err", err)
		os.Exit(1)
	}

	if err := srv.Start(ctx); err != nil {
		slog.Error("failed to start server", "err", err)
		os.Exit(1)
	}

	signal.Ignore(syscall.SIGTSTP)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	slog.Info("shutting down")
	srv.Stop()
	slog.Info("bye")
}

func overrideFromFlags(cfg *config.Config) {
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "port":
			cfg.Port = *flagPort
		case "bind":
			cfg.Bind = *flagBind
		case "auth":
			cfg.Password = *flagPassword
		case "auth-user":
			cfg.Username = *flagUsername
		case "pool-size":
			cfg.PoolSize = *flagPoolSize
		case "log-level":
			cfg.LogLevel = *flagLogLevel
		}
	})
}

func initLogger(level string) {
	var l slog.Level
	switch strings.ToLower(level) {
	case "debug":
		l = slog.LevelDebug
	case "warn", "warning":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l})))
}
