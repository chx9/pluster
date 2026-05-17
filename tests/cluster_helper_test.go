package integration

import (
	"bufio"
	"context"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pluster/pluster/pkg/config"
	"github.com/pluster/pluster/pkg/server"
)

const (
	clusterBasePort = 18000
	proxyBasePort   = 17777
)

var sharedCluster *TestCluster
var sharedProxy *TestProxy

func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "pluster-integration-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmpDir)

	setupDone := make(chan struct{})
	go func() {
		select {
		case <-setupDone:
		case <-time.After(60 * time.Second):
			fmt.Fprintln(os.Stderr, "FATAL: cluster setup timed out after 60s")
			os.Exit(1)
		}
	}()

	total := 3 * 2
	nodes := make([]*ClusterNode, total)
	ports := make([]int, total)
	for i := 0; i < total; i++ {
		ports[i] = findFreePort(clusterBasePort + i*3)
	}
	for i := 0; i < total; i++ {
		dir := filepath.Join(tmpDir, strconv.Itoa(ports[i]))
		os.MkdirAll(dir, 0755)
		nodes[i] = &ClusterNode{Port: ports[i], Dir: dir}
	}
	for _, n := range nodes {
		if err := startRedisNode(n); err != nil {
			panic("start node: " + err.Error())
		}
	}
	time.Sleep(500 * time.Millisecond)
	if err := createCluster(nodes, 3, 1); err != nil {
		for _, n := range nodes {
			stopRedisNode(n)
		}
		panic("create cluster: " + err.Error())
	}
	time.Sleep(2 * time.Second)
	for i := 0; i < 3; i++ {
		nodes[i].IsMaster = true
	}
	sharedCluster = &TestCluster{Nodes: nodes, Masters: nodes[:3], tmpDir: tmpDir}
	close(setupDone)

	sharedProxy = newSharedProxy(sharedCluster)

	code := m.Run()

	sharedProxy.srv.Stop()
	sharedCluster.Stop()
	os.Exit(code)
}

type ClusterNode struct {
	Port    int
	Dir     string
	Cmd     *exec.Cmd
	IsMaster bool
}

type TestCluster struct {
	Nodes   []*ClusterNode
	Masters []*ClusterNode
	tmpDir  string
}

type TestProxy struct {
	srv    *server.Server
	port   int
	cancel context.CancelFunc
}

func (p *TestProxy) Addr() string {
	return fmt.Sprintf("127.0.0.1:%d", p.port)
}

func (p *TestProxy) Stop() {
	p.cancel()
	p.srv.Stop()
}

func (p *TestProxy) BlockingPoolReuses() int {
	return p.srv.BlockingPoolReuses()
}

func findFreePort(base int) int {
	for p := base; p < base+2000; p++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			ln.Close()
			return p
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err == nil {
		addr := ln.Addr().(*net.TCPAddr)
		ln.Close()
		return addr.Port
	}
	return base + rand.Intn(1000)
}

func NewTestCluster(t *testing.T, masters, replicasPerMaster int) *TestCluster {
	t.Helper()
	total := masters * (1 + replicasPerMaster)
	tmpDir := t.TempDir()

	nodes := make([]*ClusterNode, total)
	ports := make([]int, total)
	for i := 0; i < total; i++ {
		ports[i] = findFreePort(clusterBasePort + i*3)
	}

	for i := 0; i < total; i++ {
		dir := filepath.Join(tmpDir, strconv.Itoa(ports[i]))
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		nodes[i] = &ClusterNode{Port: ports[i], Dir: dir}
	}

	for _, n := range nodes {
		if err := startRedisNode(n); err != nil {
			t.Fatalf("start node %d: %v", n.Port, err)
		}
	}

	time.Sleep(500 * time.Millisecond)

	if err := createCluster(nodes, masters, replicasPerMaster); err != nil {
		for _, n := range nodes {
			stopRedisNode(n)
		}
		t.Fatalf("create cluster: %v", err)
	}

	time.Sleep(1 * time.Second)

	masterNodes := nodes[:masters]
	for _, n := range masterNodes {
		n.IsMaster = true
	}

	c := &TestCluster{Nodes: nodes, Masters: masterNodes, tmpDir: tmpDir}
	t.Cleanup(func() { c.Stop() })
	return c
}

func startRedisNode(n *ClusterNode) error {
	confPath := filepath.Join(n.Dir, "redis.conf")
	conf := fmt.Sprintf(`port %d
cluster-enabled yes
cluster-config-file nodes.conf
cluster-node-timeout 2000
appendonly no
loglevel warning
dir %s
`, n.Port, n.Dir)
	if err := os.WriteFile(confPath, []byte(conf), 0644); err != nil {
		return err
	}
	cmd := exec.Command("redis-server", confPath)
	cmd.Dir = n.Dir
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("redis-server start: %w", err)
	}
	n.Cmd = cmd

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if pingRedis(n.Port) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("node %d did not start in time", n.Port)
}

func pingRedis(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
	if err != nil {
		return false
	}
	defer conn.Close()
	fmt.Fprintf(conn, "*1\r\n$4\r\nPING\r\n")
	buf := make([]byte, 7)
	conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	n, err := conn.Read(buf)
	return err == nil && n > 0 && buf[0] == '+'
}

func stopRedisNode(n *ClusterNode) {
	if n.Cmd != nil && n.Cmd.Process != nil {
		n.Cmd.Process.Kill()
		n.Cmd.Wait()
	}
}

func createCluster(nodes []*ClusterNode, masters, replicasPerMaster int) error {
	addrs := make([]string, len(nodes))
	for i, n := range nodes {
		addrs[i] = fmt.Sprintf("127.0.0.1:%d", n.Port)
	}

	args := []string{
		"--cluster", "create",
	}
	args = append(args, addrs...)
	args = append(args, "--cluster-replicas", strconv.Itoa(replicasPerMaster))

	cmd := exec.Command("redis-cli", args...)
	cmd.Stdin = strings.NewReader("yes\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("redis-cli cluster create: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "[OK]") && !strings.Contains(string(out), "All 16384 slots covered") {
		return fmt.Errorf("cluster creation may have failed:\n%s", out)
	}
	return nil
}

func (c *TestCluster) Stop() {
	for _, n := range c.Nodes {
		stopRedisNode(n)
	}
}

func (c *TestCluster) EntryPoints() []string {
	eps := make([]string, len(c.Masters))
	for i, n := range c.Masters {
		eps[i] = fmt.Sprintf("127.0.0.1:%d", n.Port)
	}
	return eps
}

func (c *TestCluster) StopNode(n *ClusterNode) {
	stopRedisNode(n)
}

func (c *TestCluster) StartNode(n *ClusterNode) error {
	return startRedisNode(n)
}

func (c *TestCluster) WaitForCluster(t *testing.T, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if c.isClusterOK() {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("cluster did not become OK in time")
}

func (c *TestCluster) isClusterOK() bool {
	for _, n := range c.Nodes {
		if !pingRedis(n.Port) {
			continue
		}
		info := redisCmd(n.Port, "CLUSTER", "INFO")
		if strings.Contains(info, "cluster_state:ok") {
			return true
		}
	}
	return false
}

func redisCmd(port int, args ...string) string {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		return ""
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))

	fmt.Fprintf(conn, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(conn, "$%d\r\n%s\r\n", len(a), a)
	}

	buf := make([]byte, 8192)
	total := 0
	for total < len(buf) {
		n, err := conn.Read(buf[total:])
		total += n
		if err != nil || total >= 4096 {
			break
		}
	}
	return string(buf[:total])
}

func newSharedProxy(cluster *TestCluster) *TestProxy {
	port := findFreePort(proxyBasePort)
	cfg := config.FromArgs(cluster.EntryPoints(), config.WithPort(port))
	cfg.Bind = "127.0.0.1"
	cfg.PoolSize = 20

	ctx, cancel := context.WithCancel(context.Background())
	srv, err := server.New(cfg)
	if err != nil {
		cancel()
		panic("create shared proxy: " + err.Error())
	}
	if err := srv.Start(ctx); err != nil {
		cancel()
		panic("start shared proxy: " + err.Error())
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

func NewTestProxy(t *testing.T, cluster *TestCluster, opts ...func(*config.Config)) *TestProxy {
	t.Helper()
	if len(opts) == 0 && cluster == sharedCluster && sharedProxy != nil {
		return sharedProxy
	}

	port := findFreePort(proxyBasePort)
	cfg := config.FromArgs(cluster.EntryPoints(),
		config.WithPort(port),
	)
	for _, o := range opts {
		o(cfg)
	}
	cfg.Bind = "127.0.0.1"
	cfg.PoolSize = 20

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

	p := &TestProxy{srv: srv, port: port, cancel: cancel}
	t.Cleanup(p.Stop)
	return p
}

type RedisConn struct {
	conn net.Conn
	r    *bufio.Reader
}

func DialProxy(t *testing.T, proxy *TestProxy) *RedisConn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", proxy.Addr(), 5*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return &RedisConn{conn: conn, r: bufio.NewReader(conn)}
}

func (c *RedisConn) Send(args ...string) {
	fmt.Fprintf(c.conn, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(c.conn, "$%d\r\n%s\r\n", len(a), a)
	}
}

func (c *RedisConn) SendBinary(args ...[]byte) {
	fmt.Fprintf(c.conn, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(c.conn, "$%d\r\n", len(a))
		c.conn.Write(a)
		fmt.Fprintf(c.conn, "\r\n")
	}
}

func (c *RedisConn) ReadLine() string {
	c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, _ := c.r.ReadString('\n')
	return strings.TrimRight(line, "\r\n")
}

func (c *RedisConn) ReadReply() string {
	line := c.ReadLine()
	if len(line) == 0 {
		return ""
	}
	switch line[0] {
	case '+', '-', ':':
		return line
	case '$':
		n, _ := strconv.Atoi(line[1:])
		if n < 0 {
			return "$-1"
		}
		buf := make([]byte, n+2)
		c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		ioReadFullBuf(c.r, buf)
		return "$" + strconv.Itoa(n) + ":" + string(buf[:n])
	case '*':
		n, _ := strconv.Atoi(line[1:])
		if n < 0 {
			return "*-1"
		}
		parts := make([]string, n)
		for i := 0; i < n; i++ {
			parts[i] = c.ReadReply()
		}
		return "*" + strconv.Itoa(n) + ":[" + strings.Join(parts, ",") + "]"
	}
	return line
}

func ioReadFullBuf(r *bufio.Reader, buf []byte) {
	total := 0
	for total < len(buf) {
		n, err := r.Read(buf[total:])
		total += n
		if err != nil {
			break
		}
	}
}

func (c *RedisConn) Do(args ...string) string {
	c.Send(args...)
	return c.ReadReply()
}

func (c *RedisConn) MustOK(t *testing.T, args ...string) {
	t.Helper()
	reply := c.Do(args...)
	if reply != "+OK" {
		t.Errorf("expected +OK, got %s (cmd: %v)", reply, args)
	}
}

func (c *RedisConn) MustGet(t *testing.T, key, expected string) {
	t.Helper()
	reply := c.Do("GET", key)
	want := "$" + strconv.Itoa(len(expected)) + ":" + expected
	if reply != want {
		t.Errorf("GET %s: expected %q, got %q", key, want, reply)
	}
}

func (c *RedisConn) MustNil(t *testing.T, key string) {
	t.Helper()
	reply := c.Do("GET", key)
	if reply != "$-1" {
		t.Errorf("GET %s: expected nil, got %s", key, reply)
	}
}
