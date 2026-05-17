package integration

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestNodeDownReturnsErrors(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping node down test in short mode")
	}
	cluster := NewTestCluster(t, 3, 1)
	cluster.WaitForCluster(t, 30*time.Second)
	proxy := NewTestProxy(t, cluster)

	c := DialProxy(t, proxy)

	for i := 0; i < 30; i++ {
		key := fmt.Sprintf("{nodedown_err}:k:%d", i)
		c.MustOK(t, "SET", key, fmt.Sprintf("val%d", i))
	}

	downNode := cluster.Masters[0]
	cluster.StopNode(downNode)
	t.Logf("stopped master node on port %d", downNode.Port)

	time.Sleep(500 * time.Millisecond)

	deadline := time.Now().Add(5 * time.Second)
	errCount := 0
	okCount := 0
	for i := 0; i < 30; i++ {
		key := fmt.Sprintf("{nodedown_err}:k:%d", i)
		c2 := DialProxy(t, proxy)
		c2.conn.SetDeadline(deadline)
		c2.Send("GET", key)
		reply := c2.ReadReply()
		if strings.HasPrefix(reply, "-") {
			errCount++
		} else {
			okCount++
		}
	}
	t.Logf("node down: %d errors, %d successes (keys on other nodes still succeed)", errCount, okCount)

	if errCount == 0 {
		t.Error("expected at least some errors when a master node is down, got none")
	}

	if err := cluster.StartNode(downNode); err != nil {
		t.Logf("restart node failed: %v", err)
	}
}

func TestNodeDownNoHang(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping node down no-hang test in short mode")
	}
	cluster := NewTestCluster(t, 3, 1)
	cluster.WaitForCluster(t, 30*time.Second)
	proxy := NewTestProxy(t, cluster)

	downNode := cluster.Masters[0]
	cluster.StopNode(downNode)
	t.Logf("stopped master node on port %d", downNode.Port)

	time.Sleep(200 * time.Millisecond)

	downAddr := fmt.Sprintf("127.0.0.1:%d", downNode.Port)
	topo := proxy.srv.TopoManager().LoadTopo()
	downSlot := -1
	for slot := 0; slot < 16384; slot++ {
		node := topo.GetNodeForSlot(slot)
		if node != nil && node.Addr == downAddr {
			downSlot = slot
			break
		}
	}

	if downSlot < 0 {
		t.Skip("could not find a slot owned by the stopped node (may have been re-assigned already)")
		return
	}

	slotKey := crc16SlotKey(downSlot)
	testKey := fmt.Sprintf("k:{%s}:hang_test", slotKey)

	const maxWait = 8 * time.Second
	done := make(chan string, 1)
	go func() {
		conn := DialProxy(t, proxy)
		conn.Send("GET", testKey)
		reply := conn.ReadReply()
		done <- reply
	}()

	select {
	case reply := <-done:
		if !strings.HasPrefix(reply, "-") && reply != "$-1" {
			t.Errorf("expected error or nil for key on down node, got %q", reply)
		}
		t.Logf("got reply %q within timeout (no hang)", reply)
	case <-time.After(maxWait):
		t.Errorf("proxy hung for >%v when backend node is down", maxWait)
	}

	if err := cluster.StartNode(downNode); err != nil {
		t.Logf("restart node failed: %v", err)
	}
}

func TestNodeDownPipelineRobustness(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping node down pipeline test in short mode")
	}
	cluster := NewTestCluster(t, 3, 1)
	cluster.WaitForCluster(t, 30*time.Second)
	proxy := NewTestProxy(t, cluster)

	c := DialProxy(t, proxy)
	for i := 0; i < 20; i++ {
		key := fmt.Sprintf("{ndpipe}:k:%d", i)
		c.MustOK(t, "SET", key, fmt.Sprintf("v%d", i))
	}

	downNode := cluster.Masters[0]
	cluster.StopNode(downNode)
	t.Logf("stopped master node on port %d", downNode.Port)
	time.Sleep(300 * time.Millisecond)

	piper := DialProxy(t, proxy)
	for i := 0; i < 20; i++ {
		key := fmt.Sprintf("{ndpipe}:k:%d", i)
		piper.Send("GET", key)
	}

	const replyTimeout = 10 * time.Second
	replies := make([]string, 20)
	for i := 0; i < 20; i++ {
		piper.conn.SetReadDeadline(time.Now().Add(replyTimeout))
		replies[i] = piper.ReadReply()
		if replies[i] == "" {
			t.Errorf("pipeline command %d: got empty reply (possible hang)", i)
		}
	}

	for i, r := range replies {
		if r == "" {
			t.Errorf("reply[%d] is empty (timeout/hang)", i)
		}
	}
	t.Logf("all 20 pipelined replies received (no hang)")

	if err := cluster.StartNode(downNode); err != nil {
		t.Logf("restart node failed: %v", err)
	}
	for i := 0; i < 20; i++ {
		c.Do("DEL", fmt.Sprintf("{ndpipe}:k:%d", i))
	}
}

func TestNodeDownRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping node down recovery test in short mode")
	}
	cluster := NewTestCluster(t, 3, 1)
	cluster.WaitForCluster(t, 30*time.Second)
	proxy := NewTestProxy(t, cluster)

	downNode := cluster.Masters[0]
	cluster.StopNode(downNode)
	t.Logf("stopped master on port %d", downNode.Port)
	time.Sleep(2 * time.Second)

	if err := cluster.StartNode(downNode); err != nil {
		t.Logf("restart node failed: %v (skipping recovery check)", err)
		return
	}
	t.Logf("restarted master on port %d", downNode.Port)

	cluster.WaitForCluster(t, 30*time.Second)
	time.Sleep(6 * time.Second)

	c := DialProxy(t, proxy)
	for i := 0; i < 20; i++ {
		key := fmt.Sprintf("ndrecov:k:%d", i)
		reply := c.Do("SET", key, fmt.Sprintf("val%d", i))
		if reply != "+OK" {
			t.Errorf("after recovery SET %s: got %s", key, reply)
		}
	}

	for i := 0; i < 20; i++ {
		key := fmt.Sprintf("ndrecov:k:%d", i)
		c.Do("DEL", key)
	}
}
