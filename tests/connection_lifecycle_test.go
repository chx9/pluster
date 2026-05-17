package integration

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/pluster/pluster/pkg/config"
	"github.com/pluster/pluster/pkg/server"
)

// startFreshProxy starts a proxy but returns as soon as the TCP listener is up,
// before backend connections are established. Commands sent immediately will
// queue in the dialing map and be flushed in onOpen — the path not exercised
// by tests that use the shared pre-warmed proxy.
func startFreshProxy(t *testing.T, cluster *TestCluster, opts ...func(*config.Config)) *TestProxy {
	t.Helper()
	port := findFreePort(proxyBasePort + 500)
	cfg := config.FromArgs(cluster.EntryPoints(), config.WithPort(port))
	for _, o := range opts {
		o(cfg)
	}
	cfg.Bind = "127.0.0.1"
	cfg.PoolSize = 5

	ctx, cancel := context.WithCancel(context.Background())
	srv, err := server.New(cfg)
	if err != nil {
		cancel()
		t.Fatalf("create fresh proxy: %v", err)
	}
	if err := srv.Start(ctx); err != nil {
		cancel()
		t.Fatalf("start fresh proxy: %v", err)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			c.Close()
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	p := &TestProxy{srv: srv, port: port, cancel: cancel}
	t.Cleanup(p.Stop)
	return p
}

func TestFreshProxyFirstCommand(t *testing.T) {
	proxy := startFreshProxy(t, sharedCluster)

	c := DialProxy(t, proxy)
	c.conn.SetDeadline(time.Now().Add(10 * time.Second))

	if reply := c.Do("PING"); reply != "+PONG" {
		t.Fatalf("first PING on fresh proxy: expected +PONG, got %q", reply)
	}
	c.MustOK(t, "SET", "fresh:first", "value")
	c.MustGet(t, "fresh:first", "value")
	c.Do("DEL", "fresh:first")
}

func TestFreshProxyPipelineOnDial(t *testing.T) {
	proxy := startFreshProxy(t, sharedCluster)

	c := DialProxy(t, proxy)
	c.conn.SetDeadline(time.Now().Add(15 * time.Second))

	const n = 20
	for i := 0; i < n; i++ {
		c.Send("SET", fmt.Sprintf("{dial}:k:%d", i), strconv.Itoa(i))
	}
	for i := 0; i < n; i++ {
		if reply := c.ReadReply(); reply != "+OK" {
			t.Errorf("pipeline SET %d on fresh proxy: expected +OK, got %q", i, reply)
		}
	}

	for i := 0; i < n; i++ {
		c.Send("GET", fmt.Sprintf("{dial}:k:%d", i))
	}
	for i := 0; i < n; i++ {
		reply := c.ReadReply()
		want := "$" + strconv.Itoa(len(strconv.Itoa(i))) + ":" + strconv.Itoa(i)
		if reply != want {
			t.Errorf("pipeline GET %d: expected %s, got %q", i, want, reply)
		}
	}

	for i := 0; i < n; i++ {
		c.Do("DEL", fmt.Sprintf("{dial}:k:%d", i))
	}
}

func TestConcurrentClientsOnFreshProxy(t *testing.T) {
	proxy := startFreshProxy(t, sharedCluster)

	const numClients = 20
	const opsPerClient = 10

	var wg sync.WaitGroup
	errs := make(chan string, numClients*opsPerClient)

	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			c := DialProxy(t, proxy)
			c.conn.SetDeadline(time.Now().Add(15 * time.Second))

			for j := 0; j < opsPerClient; j++ {
				key := fmt.Sprintf("{concurrent}:c%d:k%d", id, j)
				val := fmt.Sprintf("v%d_%d", id, j)
				if r := c.Do("SET", key, val); r != "+OK" {
					errs <- fmt.Sprintf("client %d SET %s: got %q", id, key, r)
					return
				}
				r := c.Do("GET", key)
				want := "$" + strconv.Itoa(len(val)) + ":" + val
				if r != want {
					errs <- fmt.Sprintf("client %d GET %s: want %q got %q", id, key, want, r)
					return
				}
			}
			for j := 0; j < opsPerClient; j++ {
				c.Do("DEL", fmt.Sprintf("{concurrent}:c%d:k%d", id, j))
			}
		}(i)
	}

	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

func TestProxyRestartRespondsImmediately(t *testing.T) {
	makeProxy := func() *TestProxy {
		port := findFreePort(proxyBasePort + 600)
		cfg := config.FromArgs(sharedCluster.EntryPoints(), config.WithPort(port))
		cfg.Bind = "127.0.0.1"
		cfg.PoolSize = 5
		ctx, cancel := context.WithCancel(context.Background())
		srv, err := server.New(cfg)
		if err != nil {
			cancel()
			t.Fatalf("create proxy: %v", err)
		}
		if err := srv.Start(ctx); err != nil {
			cancel()
			t.Fatalf("start proxy: %v", err)
		}
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			c, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
			if err == nil {
				c.Close()
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		return &TestProxy{srv: srv, port: port, cancel: cancel}
	}

	p1 := makeProxy()
	c1 := DialProxy(t, p1)
	c1.conn.SetDeadline(time.Now().Add(5 * time.Second))
	if r := c1.Do("PING"); r != "+PONG" {
		t.Fatalf("first proxy PING: expected +PONG, got %q", r)
	}
	p1.Stop()
	c1.conn.Close()

	p2 := makeProxy()
	defer p2.Stop()

	done := make(chan string, 1)
	go func() {
		conn, err := net.DialTimeout("tcp", p2.Addr(), 3*time.Second)
		if err != nil {
			done <- fmt.Sprintf("dial error: %v", err)
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		fmt.Fprintf(conn, "*1\r\n$4\r\nPING\r\n")
		buf := make([]byte, 7)
		n, err := conn.Read(buf)
		if err != nil || n == 0 {
			done <- fmt.Sprintf("read error: %v (n=%d)", err, n)
			return
		}
		done <- string(buf[:n])
	}()

	select {
	case reply := <-done:
		if reply != "+PONG\r\n" {
			t.Errorf("second proxy PING: expected +PONG\\r\\n, got %q", reply)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("second proxy blocked for >8s")
	}
}

func TestRapidNewConnections(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	const numConns = 50
	errs := make(chan string, numConns)
	var wg sync.WaitGroup

	for i := 0; i < numConns; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			conn, err := net.DialTimeout("tcp", proxy.Addr(), 3*time.Second)
			if err != nil {
				errs <- fmt.Sprintf("conn %d dial: %v", id, err)
				return
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(5 * time.Second))
			fmt.Fprintf(conn, "*1\r\n$4\r\nPING\r\n")
			buf := make([]byte, 7)
			n, err := conn.Read(buf)
			if err != nil || n == 0 || buf[0] != '+' {
				errs <- fmt.Sprintf("conn %d PING: n=%d err=%v reply=%q", id, n, err, string(buf[:n]))
			}
		}(i)
	}

	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}

	c := DialProxy(t, proxy)
	c.conn.SetDeadline(time.Now().Add(5 * time.Second))
	if r := c.Do("PING"); r != "+PONG" {
		t.Errorf("proxy after connection storm: expected +PONG, got %q", r)
	}
}

// TestDialingQueueDataIntegrity targets the exact bug class from commit 7ebb883:
// requests queued in the dialing map must not alias a reused parse buffer.
// Distinct long values make corruption visible as wrong content, not just crashes.
func TestDialingQueueDataIntegrity(t *testing.T) {
	proxy := startFreshProxy(t, sharedCluster)

	const numKeys = 30
	type kv struct{ key, val string }
	kvs := make([]kv, numKeys)
	for i := range kvs {
		kvs[i] = kv{
			key: fmt.Sprintf("{dialq}:k:%d", i),
			val: fmt.Sprintf("value-with-distinct-content-%d-abcdefgh", i),
		}
	}

	c := DialProxy(t, proxy)
	c.conn.SetDeadline(time.Now().Add(15 * time.Second))

	for _, kv := range kvs {
		c.Send("SET", kv.key, kv.val)
	}
	for i := range kvs {
		if reply := c.ReadReply(); reply != "+OK" {
			t.Fatalf("SET %s: expected +OK, got %q", kvs[i].key, reply)
		}
	}

	for _, kv := range kvs {
		c.Send("GET", kv.key)
	}
	for _, kv := range kvs {
		reply := c.ReadReply()
		want := "$" + strconv.Itoa(len(kv.val)) + ":" + kv.val
		if reply != want {
			t.Errorf("GET %s: expected %q, got %q", kv.key, want, reply)
		}
	}
	for _, kv := range kvs {
		c.Do("DEL", kv.key)
	}
}
