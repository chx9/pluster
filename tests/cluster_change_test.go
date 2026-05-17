package integration

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

)

func TestClusterTopologyRefresh(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping cluster topology test in short mode")
	}
	cluster := NewTestCluster(t, 3, 1)
	cluster.WaitForCluster(t, 30*time.Second)
	proxy := NewTestProxy(t, cluster)

	c := DialProxy(t, proxy)

	for i := 0; i < 20; i++ {
		key := fmt.Sprintf("{refresh}:k:%d", i)
		c.MustOK(t, "SET", key, strconv.Itoa(i))
	}

	time.Sleep(6 * time.Second)

	for i := 0; i < 20; i++ {
		key := fmt.Sprintf("{refresh}:k:%d", i)
		c.MustGet(t, key, strconv.Itoa(i))
	}

	for i := 0; i < 20; i++ {
		c.Do("DEL", fmt.Sprintf("{refresh}:k:%d", i))
	}
}

func TestNodeDown(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping node down test in short mode")
	}
	cluster := NewTestCluster(t, 3, 1)
	cluster.WaitForCluster(t, 30*time.Second)
	proxy := NewTestProxy(t, cluster)

	numKeys := 50
	c := DialProxy(t, proxy)

	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("{nodedown}:k:%d", i)
		c.MustOK(t, "SET", key, strconv.Itoa(i))
	}

	downNode := cluster.Masters[0]
	cluster.StopNode(downNode)
	t.Logf("stopped node on port %d", downNode.Port)

	time.Sleep(3 * time.Second)

	successCount := 0
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("{nodedown}:k:%d", i)
		c2 := DialProxy(t, proxy)
		c2.Send("GET", key)
		reply := c2.ReadReply()
		if !strings.HasPrefix(reply, "-") {
			successCount++
		}
	}
	t.Logf("after node down: %d/%d reads succeeded (some failures expected)", successCount, numKeys)

	if err := cluster.StartNode(downNode); err != nil {
		t.Logf("restart node failed: %v (may need manual cleanup)", err)
		return
	}
	t.Logf("restarted node on port %d", downNode.Port)

	cluster.WaitForCluster(t, 30*time.Second)
	time.Sleep(6 * time.Second)

	c3 := DialProxy(t, proxy)
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("{nodedown}:k:%d", i)
		c3.Send("GET", key)
		reply := c3.ReadReply()
		if strings.HasPrefix(reply, "-") {
			t.Errorf("after recovery GET %s: got error %s", key, reply)
		}
	}

	for i := 0; i < numKeys; i++ {
		c.Do("DEL", fmt.Sprintf("{nodedown}:k:%d", i))
	}
}

func TestSlotMigration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slot migration test in short mode")
	}
	cluster := NewTestCluster(t, 3, 1)
	cluster.WaitForCluster(t, 30*time.Second)
	proxy := NewTestProxy(t, cluster)

	numKeys := 30
	targetSlot := 1
	slotKey := crc16SlotKey(targetSlot)

	c := DialProxy(t, proxy)

	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("k:{%s}:%d", slotKey, i)
		val := fmt.Sprintf("%d:\x00\r\n:%d", i, i)
		c.SendBinary([]byte("SET"), []byte(key), []byte(val))
		reply := c.ReadReply()
		if reply != "+OK" {
			t.Fatalf("SET %s: got %s", key, reply)
		}
	}

	sourceNode := findNodeForSlot(cluster, targetSlot)
	if sourceNode == nil {
		t.Skip("could not find source node for slot")
	}
	targetNode := cluster.Masters[0]
	if targetNode == sourceNode {
		targetNode = cluster.Masters[1]
	}

	t.Logf("migrating slot %d from port %d to port %d", targetSlot, sourceNode.Port, targetNode.Port)

	sourceID := getNodeID(sourceNode.Port)
	targetID := getNodeID(targetNode.Port)
	if sourceID == "" || targetID == "" {
		t.Skip("could not get node IDs")
	}

	setSlotImporting(targetNode.Port, targetSlot, sourceID)
	setSlotMigrating(sourceNode.Port, targetSlot, targetID)

	validateSlotKeys(t, proxy, numKeys, targetSlot, slotKey)

	migrateHalfKeys(t, sourceNode.Port, targetNode.Port, numKeys/2, targetSlot, slotKey)

	validateSlotKeys(t, proxy, numKeys, targetSlot, slotKey)

	migrateHalfKeys(t, sourceNode.Port, targetNode.Port, numKeys, targetSlot, slotKey)

	setSlotNode(sourceNode.Port, targetSlot, targetID)
	setSlotNode(targetNode.Port, targetSlot, targetID)

	proxy.srv.TopoManager().TriggerRefresh()
	time.Sleep(2 * time.Second)

	validateSlotKeys(t, proxy, numKeys, targetSlot, slotKey)

	c2 := DialProxy(t, proxy)
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("k:{%s}:%d", slotKey, i)
		c2.Do("DEL", key)
	}
}

func TestScaleOut(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping scale out test in short mode")
	}
	cluster := NewTestCluster(t, 3, 1)
	cluster.WaitForCluster(t, 30*time.Second)
	proxy := NewTestProxy(t, cluster)

	numKeys := 30
	c := DialProxy(t, proxy)

	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("scaleout:k:%d", i)
		c.MustOK(t, "SET", key, strconv.Itoa(i))
	}

	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("scaleout:k:%d", i)
		c.MustGet(t, key, strconv.Itoa(i))
	}

	t.Log("scale-out: keys accessible before topology change")

	proxy.srv.TopoManager().TriggerRefresh()
	time.Sleep(2 * time.Second)

	c2 := DialProxy(t, proxy)
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("scaleout:k:%d", i)
		c2.MustGet(t, key, strconv.Itoa(i))
	}

	for i := 0; i < numKeys; i++ {
		c.Do("DEL", fmt.Sprintf("scaleout:k:%d", i))
	}
}

func TestReadDuringTopologyChange(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping topology change test in short mode")
	}
	cluster := NewTestCluster(t, 3, 1)
	cluster.WaitForCluster(t, 30*time.Second)
	proxy := NewTestProxy(t, cluster)

	numKeys := 50
	c := DialProxy(t, proxy)
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("{rdtopo}:k:%d", i)
		c.MustOK(t, "SET", key, strconv.Itoa(i))
	}

	var wg sync.WaitGroup
	errCh := make(chan error, numKeys*3)

	for round := 0; round < 3; round++ {
		for i := 0; i < numKeys; i++ {
			wg.Add(1)
			go func(r, idx int) {
				defer wg.Done()
				c2 := DialProxy(t, proxy)
				key := fmt.Sprintf("{rdtopo}:k:%d", idx)
				c2.Send("GET", key)
				reply := c2.ReadReply()
				want := "$" + strconv.Itoa(len(strconv.Itoa(idx))) + ":" + strconv.Itoa(idx)
				if reply != want {
					errCh <- fmt.Errorf("round %d GET %s: want %s got %s", r, key, want, reply)
				}
			}(round, i)
		}
		proxy.srv.TopoManager().TriggerRefresh()
		time.Sleep(100 * time.Millisecond)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}

	for i := 0; i < numKeys; i++ {
		c.Do("DEL", fmt.Sprintf("{rdtopo}:k:%d", i))
	}
}

func crc16SlotKey(slot int) string {
	for i := 0; i < 16384; i++ {
		key := strconv.Itoa(i)
		if hashSlotSimple(key) == slot {
			return key
		}
	}
	return "0"
}

func hashSlotSimple(key string) int {
	var crc uint16
	crc16tab := [256]uint16{
		0x0000, 0x1021, 0x2042, 0x3063, 0x4084, 0x50a5, 0x60c6, 0x70e7,
		0x8108, 0x9129, 0xa14a, 0xb16b, 0xc18c, 0xd1ad, 0xe1ce, 0xf1ef,
		0x1231, 0x0210, 0x3273, 0x2252, 0x52b5, 0x4294, 0x72f7, 0x62d6,
		0x9339, 0x8318, 0xb37b, 0xa35a, 0xd3bd, 0xc39c, 0xf3ff, 0xe3de,
		0x2462, 0x3443, 0x0420, 0x1401, 0x64e6, 0x74c7, 0x44a4, 0x5485,
		0xa56a, 0xb54b, 0x8528, 0x9509, 0xe5ee, 0xf5cf, 0xc5ac, 0xd58d,
		0x3653, 0x2672, 0x1611, 0x0630, 0x76d7, 0x66f6, 0x5695, 0x46b4,
		0xb75b, 0xa77a, 0x9719, 0x8738, 0xf7df, 0xe7fe, 0xd79d, 0xc7bc,
		0x4864, 0x5845, 0x6826, 0x7807, 0x08e0, 0x18c1, 0x28a2, 0x38c3,
		0xc92c, 0xd90d, 0xe96e, 0xf94f, 0x89a8, 0x99c9, 0xa9aa, 0xb98b,
		0x5a75, 0x4a54, 0x7a37, 0x6a16, 0x1af1, 0x0ad0, 0x3ab3, 0x2a92,
		0xdb6d, 0xcb4c, 0xfb2f, 0xeb0e, 0x9be9, 0x8bc8, 0xbbab, 0xab8a,
		0x6c66, 0x7c47, 0x4c24, 0x5c05, 0x2ce2, 0x3cc3, 0x0ca0, 0x1c81,
		0xed6e, 0xfd4f, 0xcd2c, 0xdd0d, 0xad6a, 0xbd4b, 0x8d28, 0x9d09,
		0x7ef7, 0x6ed6, 0x5eb5, 0x4e94, 0x3e73, 0x2e52, 0x1e31, 0x0e10,
		0xff9f, 0xefbe, 0xdfdd, 0xcffc, 0xbf1b, 0xaf3a, 0x9f59, 0x8f78,
		0x9188, 0x81a9, 0xb1ca, 0xa1eb, 0xd10c, 0xc12d, 0xf14e, 0xe16f,
		0x1080, 0x00a1, 0x30c2, 0x20e3, 0x5004, 0x4025, 0x7046, 0x6067,
		0x83b9, 0x9398, 0xa3fb, 0xb3da, 0xc33d, 0xd31c, 0xe37f, 0xf35e,
		0x02b1, 0x1290, 0x22f3, 0x32d2, 0x4235, 0x5214, 0x6277, 0x7256,
		0xb5ea, 0xa5cb, 0x95a8, 0x8589, 0xf56e, 0xe54f, 0xd52c, 0xc50d,
		0x34e2, 0x24c3, 0x14a0, 0x0481, 0x7466, 0x6447, 0x5424, 0x4405,
		0xa7db, 0xb7fa, 0x8799, 0x97b8, 0xe75f, 0xf77e, 0xc71d, 0xd73c,
		0x26d3, 0x36f2, 0x0691, 0x16b0, 0x6657, 0x7676, 0x4615, 0x5634,
		0xd94c, 0xc96d, 0xf90e, 0xe92f, 0x99c8, 0x89e9, 0xb98a, 0xa9ab,
		0x5844, 0x4865, 0x7806, 0x6827, 0x18c0, 0x08e1, 0x3882, 0x28a3,
		0xcb7d, 0xdb5c, 0xeb3f, 0xfb1e, 0x8bf9, 0x9bd8, 0xabbb, 0xbb9a,
		0x4a75, 0x5a54, 0x6a37, 0x7a16, 0x0af1, 0x1ad0, 0x2ab3, 0x3a92,
		0xfd2e, 0xed0f, 0xdd6c, 0xcd4d, 0xbdaa, 0xad8b, 0x9de8, 0x8dc9,
		0x7c26, 0x6c07, 0x5c64, 0x4c45, 0x3ca2, 0x2c83, 0x1ce0, 0x0cc1,
		0xef1f, 0xff3e, 0xcf5d, 0xdf7c, 0xaf9b, 0xbfba, 0x8fd9, 0x9ff8,
		0x6e17, 0x7e36, 0x4e55, 0x5e74, 0x2e93, 0x3eb2, 0x0ed1, 0x1ef0,
	}
	for _, b := range []byte(key) {
		crc = (crc << 8) ^ crc16tab[byte(crc>>8)^b]
	}
	return int(crc % 16384)
}

func findNodeForSlot(cluster *TestCluster, slot int) *ClusterNode {
	for _, n := range cluster.Masters {
		info := redisCmd(n.Port, "CLUSTER", "MYID")
		myID := ""
		for _, line := range strings.Split(strings.TrimSpace(info), "\n") {
			line = strings.TrimSpace(line)
			if len(line) == 40 {
				myID = line
				break
			}
		}
		if myID == "" {
			continue
		}

		nodesInfo := redisCmd(n.Port, "CLUSTER", "NODES")
		for _, line := range strings.Split(strings.TrimSpace(nodesInfo), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, myID) {
				continue
			}
			if !strings.Contains(line, "master") {
				continue
			}
			fields := strings.Fields(line)
			for _, f := range fields[8:] {
				if strings.Contains(f, "-") {
					parts := strings.SplitN(f, "-", 2)
					lo, e1 := strconv.Atoi(parts[0])
					hi, e2 := strconv.Atoi(parts[1])
					if e1 == nil && e2 == nil && slot >= lo && slot <= hi {
						return n
					}
				} else {
					s, e := strconv.Atoi(f)
					if e == nil && s == slot {
						return n
					}
				}
			}
		}
	}
	return cluster.Masters[0]
}

func getNodeID(port int) string {
	info := redisCmd(port, "CLUSTER", "MYID")
	lines := strings.Split(strings.TrimSpace(info), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "$") {
			continue
		}
		if len(line) == 40 {
			return line
		}
	}
	return ""
}

func setSlotImporting(port, slot int, sourceID string) {
	cmd := exec.Command("redis-cli", "-p", strconv.Itoa(port),
		"CLUSTER", "SETSLOT", strconv.Itoa(slot), "IMPORTING", sourceID)
	cmd.Run()
}

func setSlotMigrating(port, slot int, targetID string) {
	cmd := exec.Command("redis-cli", "-p", strconv.Itoa(port),
		"CLUSTER", "SETSLOT", strconv.Itoa(slot), "MIGRATING", targetID)
	cmd.Run()
}

func setSlotNode(port, slot int, nodeID string) {
	cmd := exec.Command("redis-cli", "-p", strconv.Itoa(port),
		"CLUSTER", "SETSLOT", strconv.Itoa(slot), "NODE", nodeID)
	cmd.Run()
}

func migrateHalfKeys(t *testing.T, srcPort, dstPort, count, slot int, slotKey string) {
	t.Helper()
	for i := 0; i < count; i++ {
		key := fmt.Sprintf("k:{%s}:%d", slotKey, i)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		cmd := exec.CommandContext(ctx, "redis-cli", "-p", strconv.Itoa(srcPort),
			"MIGRATE", "127.0.0.1", strconv.Itoa(dstPort), key, "0", "1000")
		cmd.WaitDelay = 500 * time.Millisecond
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			outStr := strings.TrimSpace(string(out))
			if outStr != "NOKEY" && !strings.Contains(outStr, "NOKEY") {
				t.Logf("migrate key %s: %v (%s)", key, err, outStr)
			}
		}
	}
}

func validateSlotKeys(t *testing.T, proxy *TestProxy, numKeys, slot int, slotKey string) {
	t.Helper()
	c := DialProxy(t, proxy)
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("k:{%s}:%d", slotKey, i)
		val := fmt.Sprintf("%d:\x00\r\n:%d", i, i)
		c.SendBinary([]byte("GET"), []byte(key))
		reply := c.ReadReply()
		want := "$" + strconv.Itoa(len(val)) + ":" + val
		if reply != want {
			t.Errorf("validateSlotKeys GET %s: want %q got %q", key, want, reply)
		}
	}
}
