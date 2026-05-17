package integration

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pluster/pluster/pkg/config"
)

func TestReadModeMasterOnly(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster, config.WithReadMode(config.ReadModeMasterOnly))
	c := DialProxy(t, proxy)

	key := "{readmode}:master"
	c.Do("SET", key, "masterval")

	reply := c.Do("GET", key)
	if !strings.Contains(reply, "masterval") {
		t.Errorf("master-only GET: expected 'masterval', got %s", reply)
	}
	c.Do("DEL", key)
}

func TestReadModeMasterSlave(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster, config.WithReadMode(config.ReadModeMasterSlave))
	c := DialProxy(t, proxy)

	key := "{readmode}:masterslave"
	c.Do("SET", key, "masterslaveval")

	// master-slave round-robins across master+slaves; allow replication lag
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		reply := c.Do("GET", key)
		if strings.Contains(reply, "masterslaveval") {
			c.Do("DEL", key)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("master-slave GET: expected 'masterslaveval' within 2s, never got it")
	c.Do("DEL", key)
}

func TestReadModeMasterSlaveFallbackToMaster(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster, config.WithReadMode(config.ReadModeMasterSlave))
	c := DialProxy(t, proxy)

	for i := 0; i < 20; i++ {
		key := "{readmode:fallback}:key"
		c.Do("SET", key, "val")
		time.Sleep(10 * time.Millisecond)
		reply := c.Do("GET", key)
		if !strings.Contains(reply, "val") {
			t.Errorf("master-slave fallback: round %d: expected 'val', got %s", i, reply)
		}
		c.Do("DEL", key)
	}
}

func TestReadModeSlaveOnly(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster, config.WithReadMode(config.ReadModeSlaveOnly))
	c := DialProxy(t, proxy)

	key := "{readmode}:slave"
	c.Do("SET", key, "slaveval")

	// slave-only reads from replica; allow replication lag
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		reply := c.Do("GET", key)
		if strings.Contains(reply, "slaveval") {
			c.Do("DEL", key)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("slave-only GET: expected 'slaveval' within 2s, never got it")
	c.Do("DEL", key)
}

func TestReadModeWriteAlwaysMaster(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster, config.WithReadMode(config.ReadModeMasterSlave))
	c := DialProxy(t, proxy)

	key := "{readmode}:write"

	for i := 0; i < 10; i++ {
		reply := c.Do("SET", key, "v")
		if !strings.Contains(reply, "OK") {
			t.Errorf("SET in master-slave mode: expected OK, got %s", reply)
		}
	}
	c.Do("DEL", key)
}

func TestReadModeMultipleReadTypes(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster, config.WithReadMode(config.ReadModeMasterSlave))
	c := DialProxy(t, proxy)

	key := "{readmode}:multi"
	c.Do("DEL", key)
	c.Do("SET", key, "hello")
	time.Sleep(30 * time.Millisecond)

	tests := []struct {
		cmd      string
		args     []string
		contains string
	}{
		{"GET", []string{key}, "hello"},
		{"STRLEN", []string{key}, "5"},
		{"TYPE", []string{key}, "string"},
		{"TTL", []string{key}, "-1"},
	}

	for _, tt := range tests {
		allArgs := append([]string{tt.cmd}, tt.args...)
		reply := c.Do(allArgs...)
		if !strings.Contains(reply, tt.contains) {
			t.Errorf("%s in prefer-replica mode: expected %q, got %s", tt.cmd, tt.contains, reply)
		}
	}
	c.Do("DEL", key)
}

func parseInfoStat(info, field string) int64 {
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, field+":") {
			val := strings.TrimPrefix(line, field+":")
			val = strings.TrimSpace(val)
			n, _ := strconv.ParseInt(val, 10, 64)
			return n
		}
	}
	return -1
}

func replicaNodes(cluster *TestCluster) []*ClusterNode {
	var replicas []*ClusterNode
	for _, n := range cluster.Nodes {
		if !n.IsMaster {
			replicas = append(replicas, n)
		}
	}
	return replicas
}

func TestReadModeReplicaActuallyServesReads(t *testing.T) {
	replicas := replicaNodes(sharedCluster)
	if len(replicas) == 0 {
		t.Skip("no replica nodes in cluster")
	}

	proxy := NewTestProxy(t, sharedCluster, config.WithReadMode(config.ReadModeSlaveOnly))
	c := DialProxy(t, proxy)

	key := "{readmode}:actual"
	c.Do("SET", key, "checkval")
	time.Sleep(100 * time.Millisecond)

	beforeCmds := make([]int64, len(replicas))
	for i, r := range replicas {
		info := redisCmd(r.Port, "INFO", "stats")
		beforeCmds[i] = parseInfoStat(info, "total_commands_processed")
	}

	const reads = 20
	for i := 0; i < reads; i++ {
		reply := c.Do("GET", key)
		if !strings.Contains(reply, "checkval") {
			t.Errorf("read %d: expected checkval, got %s", i, reply)
		}
	}

	time.Sleep(50 * time.Millisecond)

	totalReplicaIncrease := int64(0)
	for i, r := range replicas {
		info := redisCmd(r.Port, "INFO", "stats")
		after := parseInfoStat(info, "total_commands_processed")
		if after > beforeCmds[i] {
			totalReplicaIncrease += after - beforeCmds[i]
		}
	}

	if totalReplicaIncrease < int64(reads) {
		t.Errorf("replica-only mode: expected at least %d commands on replicas, got %d", reads, totalReplicaIncrease)
	}

	c.Do("DEL", key)
}

func TestReadModeConfigParsing(t *testing.T) {
	tests := []struct {
		input    string
		expected config.ReadMode
		ok       bool
	}{
		{"master-only", config.ReadModeMasterOnly, true},
		{"master-slave", config.ReadModeMasterSlave, true},
		{"slave-only", config.ReadModeSlaveOnly, true},
		{"invalid", config.ReadModeMasterOnly, false},
	}

	for _, tt := range tests {
		m, ok := config.ParseReadMode(tt.input)
		if ok != tt.ok {
			t.Errorf("ParseReadMode(%q): ok=%v, want %v", tt.input, ok, tt.ok)
		}
		if ok && m != tt.expected {
			t.Errorf("ParseReadMode(%q): mode=%v, want %v", tt.input, m, tt.expected)
		}
	}
}
