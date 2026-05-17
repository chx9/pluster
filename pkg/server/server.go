package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"runtime"
	"sync/atomic"
	"time"

	gnet "github.com/panjf2000/gnet/v2"

	"github.com/pluster/pluster/pkg/cluster"
	"github.com/pluster/pluster/pkg/config"
	"github.com/pluster/pluster/pkg/engine"
	"github.com/pluster/pluster/pkg/pool"
	"github.com/pluster/pluster/pkg/router"
)

type warnLogger struct{}

func (warnLogger) Debugf(string, ...any) {}
func (warnLogger) Infof(string, ...any)  {}
func (warnLogger) Warnf(f string, a ...any) {
	slog.Warn(fmt.Sprintf(f, a...))
}
func (warnLogger) Errorf(f string, a ...any) {
	slog.Error(fmt.Sprintf(f, a...))
}
func (warnLogger) Fatalf(f string, a ...any) {
	slog.Error("FATAL: "+fmt.Sprintf(f, a...))
}

type Server struct {
	cfg     *config.Config
	topoMgr *cluster.Manager
	poolMgr *pool.Manager
	handler *engine.ProxyHandler
	router  *router.Router
	addr    string
	ready   chan struct{}
	stopped atomic.Bool
}

func New(cfg *config.Config) (*Server, error) {
	if len(cfg.EntryPoints) == 0 {
		return nil, fmt.Errorf("no cluster entry points configured")
	}

	topoMgr := cluster.NewManager(cfg.EntryPoints, cfg.Username, cfg.Password)
	poolMgr := pool.NewManager(cfg.Username, cfg.Password, cfg.PoolSize)
	r := router.New(topoMgr.LoadTopo(), poolMgr)
	r.SetTopoRefresher(topoMgr)
	r.SetReadMode(cfg.ReadMode)

	topoMgr.SetNodeRemovedHook(func(removedAddrs []string) {
		for _, addr := range removedAddrs {
			poolMgr.RemovePool(addr)
			r.PipeMgr().RemovePool(addr)
		}
	})

	h, err := engine.NewProxyHandler(cfg, topoMgr, r)
	if err != nil {
		return nil, fmt.Errorf("create proxy handler: %w", err)
	}

	return &Server{
		cfg:     cfg,
		topoMgr: topoMgr,
		poolMgr: poolMgr,
		router:  r,
		handler: h,
		ready:   make(chan struct{}),
	}, nil
}

func (s *Server) Start(ctx context.Context) error {
	if err := s.topoMgr.Start(ctx); err != nil {
		return fmt.Errorf("cluster topology init: %w", err)
	}

	addr := s.cfg.Addr()

	ln, err := net.Listen("tcp4", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	s.addr = ln.Addr().String()
	ln.Close()

	s.handler.SetListenAddr(s.addr)

	numLoops := s.cfg.Workers
	if numLoops <= 0 {
		numLoops = runtime.NumCPU()
		if numLoops > 8 {
			numLoops = 8
		}
	}

	errCh := make(chan error, 1)
	go func() {
		err := gnet.Run(
			s.handler,
			"tcp4://"+s.addr,
			gnet.WithNumEventLoop(numLoops),
			gnet.WithReusePort(true),
			gnet.WithLogger(warnLogger{}),
		)
		if err != nil && !s.stopped.Load() {
			errCh <- err
		}
	}()

	bootCh := make(chan struct{})
	go func() {
		s.handler.WaitBoot()
		close(bootCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-bootCh:
		slog.Info("pluster listening", "addr", s.addr)
		return nil
	}
}

func (s *Server) Stop() {
	s.stopped.Store(true)
	if eng := s.handler.Engine(); eng != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = eng.Stop(ctx)
	}
	s.router.Close()
	s.topoMgr.Stop()
	s.poolMgr.Close()
}

func (s *Server) Router() *router.Router {
	return s.router
}

func (s *Server) TopoManager() *cluster.Manager {
	return s.topoMgr
}

func (s *Server) NumClients() int64 {
	return s.handler.NumClients()
}

func (s *Server) BlockingPoolReuses() int {
	return s.handler.BlockingPoolReuses()
}

func (s *Server) Addr() string {
	if s.addr != "" {
		return s.addr
	}
	return s.cfg.Addr()
}
