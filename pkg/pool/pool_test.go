package pool

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"
)

func startEchoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				buf := make([]byte, 4096)
				for {
					n, err := c.Read(buf)
					if err != nil {
						c.Close()
						return
					}
					if _, err := c.Write(buf[:n]); err != nil {
						c.Close()
						return
					}
				}
			}(conn)
		}
	}()
	return ln.Addr().String()
}

func TestPoolGetPut(t *testing.T) {
	addr := startEchoServer(t)
	p := New(Options{Addr: addr, MaxSize: 3})
	defer p.Close()

	ctx := context.Background()
	c, err := p.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	total, idle := p.Stats()
	if total != 1 || idle != 0 {
		t.Errorf("after Get: total=%d idle=%d, want 1/0", total, idle)
	}

	if err := c.Close(); err != nil {
		t.Fatalf("Close (put back): %v", err)
	}

	total, idle = p.Stats()
	if total != 1 || idle != 1 {
		t.Errorf("after Put: total=%d idle=%d, want 1/1", total, idle)
	}
}

func TestPoolReuseIdleConn(t *testing.T) {
	addr := startEchoServer(t)
	p := New(Options{Addr: addr, MaxSize: 3})
	defer p.Close()

	ctx := context.Background()
	c1, _ := p.Get(ctx)
	_ = c1.Close()

	c2, err := p.Get(ctx)
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if c1.Conn != c2.Conn {
		t.Error("expected idle conn to be reused")
	}
	_ = c2.Close()
}

func TestPoolMaxSizeBlocking(t *testing.T) {
	addr := startEchoServer(t)
	p := New(Options{Addr: addr, MaxSize: 2})
	defer p.Close()

	ctx := context.Background()
	c1, _ := p.Get(ctx)
	c2, _ := p.Get(ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		ctx2, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		c3, err := p.Get(ctx2)
		if err == nil {
			_ = c3.Close()
		}
	}()

	time.Sleep(50 * time.Millisecond)
	_ = c1.Close()
	_ = c2.Close()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Error("blocked Get did not unblock after conn returned")
	}
}

func TestPoolContextCancellation(t *testing.T) {
	addr := startEchoServer(t)
	p := New(Options{Addr: addr, MaxSize: 1})
	defer p.Close()

	ctx := context.Background()
	c, _ := p.Get(ctx)

	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := p.Get(cancelCtx)
	if err == nil {
		t.Error("expected error for cancelled context, got nil")
	}

	_ = c.Close()
}

func TestPoolClosedError(t *testing.T) {
	addr := startEchoServer(t)
	p := New(Options{Addr: addr, MaxSize: 2})
	p.Close()

	_, err := p.Get(context.Background())
	if err != ErrPoolClosed {
		t.Errorf("expected ErrPoolClosed, got %v", err)
	}
}

func TestPoolBrokenConn(t *testing.T) {
	addr := startEchoServer(t)
	p := New(Options{Addr: addr, MaxSize: 2})
	defer p.Close()

	ctx := context.Background()
	c, _ := p.Get(ctx)
	c.MarkBroken()
	_ = c.Close()

	total, idle := p.Stats()
	if idle != 0 {
		t.Errorf("broken conn should not be returned to idle pool, idle=%d", idle)
	}
	if total != 0 {
		t.Errorf("broken conn should decrement total, total=%d", total)
	}
}

func TestPoolIdleTimeout(t *testing.T) {
	addr := startEchoServer(t)
	p := New(Options{Addr: addr, MaxSize: 3, IdleTimeout: 50 * time.Millisecond})
	defer p.Close()

	ctx := context.Background()
	c, _ := p.Get(ctx)
	_ = c.Close()

	time.Sleep(100 * time.Millisecond)

	c2, err := p.Get(ctx)
	if err != nil {
		t.Fatalf("Get after idle timeout: %v", err)
	}
	_ = c2.Close()

	total, _ := p.Stats()
	if total == 0 {
		t.Error("expected a new connection to be created after idle timeout eviction")
	}
}

func TestPoolConcurrentGetPut(t *testing.T) {
	addr := startEchoServer(t)
	p := New(Options{Addr: addr, MaxSize: 5})
	defer p.Close()

	var wg sync.WaitGroup
	errs := make(chan error, 50)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			c, err := p.Get(ctx)
			if err != nil {
				errs <- err
				return
			}
			time.Sleep(5 * time.Millisecond)
			_ = c.Close()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent Get/Put: %v", err)
	}

	total, _ := p.Stats()
	if total > 5 {
		t.Errorf("pool exceeded maxSize: total=%d", total)
	}
}

func TestManagerGetPool(t *testing.T) {
	addr := startEchoServer(t)
	m := NewManager("", "", 3)
	defer m.Close()

	p1 := m.GetPool(addr)
	p2 := m.GetPool(addr)
	if p1 != p2 {
		t.Error("GetPool should return same pool for same addr")
	}

	otherAddr := "127.0.0.1:19999"
	p3 := m.GetPool(otherAddr)
	if p3 == p1 {
		t.Error("GetPool should return different pool for different addr")
	}
}

func TestManagerRemovePool(t *testing.T) {
	addr := startEchoServer(t)
	m := NewManager("", "", 3)
	defer m.Close()

	p1 := m.GetPool(addr)
	m.RemovePool(addr)

	p2 := m.GetPool(addr)
	if p1 == p2 {
		t.Error("after RemovePool, GetPool should return a new pool")
	}
}

func TestManagerClose(t *testing.T) {
	addr := startEchoServer(t)
	m := NewManager("", "", 3)

	p := m.GetPool(addr)
	ctx := context.Background()
	c, err := p.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	_ = c.Close()

	m.Close()

	_, err = p.Get(ctx)
	if err != ErrPoolClosed {
		t.Errorf("after Manager.Close, pool.Get should return ErrPoolClosed, got %v", err)
	}
}

func TestPoolDialFailure(t *testing.T) {
	p := New(Options{Addr: "127.0.0.1:1", MaxSize: 2, DialTimeout: 100 * time.Millisecond})
	defer p.Close()

	_, err := p.Get(context.Background())
	if err == nil {
		t.Error("expected dial error for unreachable addr, got nil")
	}

	total, _ := p.Stats()
	if total != 0 {
		t.Errorf("failed dial should not increment total, got %d", total)
	}
}

func TestPipelinedManagerRemovePool(t *testing.T) {
	m := NewPipelinedManager("", "", 2)
	defer m.Close()

	addr := "127.0.0.1:19998"
	p1 := m.GetPool(addr)
	p1ro := m.GetReadonlyPool(addr)
	if p1 == nil || p1ro == nil {
		t.Fatal("expected non-nil pools")
	}

	m.RemovePool(addr)

	p2 := m.GetPool(addr)
	if p1 == p2 {
		t.Error("after RemovePool, GetPool should return a new pool")
	}

	p2ro := m.GetReadonlyPool(addr)
	if p1ro == p2ro {
		t.Error("after RemovePool, GetReadonlyPool should return a new pool")
	}
}
