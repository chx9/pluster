package integration

import (
	"fmt"
	"strings"
	"testing"
)

func TestBroadcastCommand(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	t.Run("BROADCAST_PING_returns_array_with_node_addrs", func(t *testing.T) {
		reply := c.Do("BROADCAST", "PING")
		if replyIsError(reply) {
			t.Fatalf("BROADCAST PING: unexpected error %s", reply)
		}
		if !strings.HasPrefix(reply, "*") {
			t.Fatalf("BROADCAST PING: expected array reply, got %s", reply)
		}
		if !strings.Contains(reply, "127.0.0.1:") {
			t.Errorf("BROADCAST PING: expected node addr in reply, got %s", reply)
		}
		if !strings.Contains(reply, "PONG") {
			t.Errorf("BROADCAST PING: expected PONG in reply, got %s", reply)
		}
	})

	t.Run("BROADCAST_each_entry_is_pair_of_addr_and_reply", func(t *testing.T) {
		reply := c.Do("BROADCAST", "DBSIZE")
		if replyIsError(reply) {
			t.Fatalf("BROADCAST DBSIZE: unexpected error %s", reply)
		}
		if !strings.HasPrefix(reply, "*") {
			t.Fatalf("BROADCAST DBSIZE: expected array, got %s", reply)
		}
		if !strings.Contains(reply, "127.0.0.1:") {
			t.Errorf("BROADCAST DBSIZE: expected node addr in each entry, got %s", reply)
		}
	})

	t.Run("BROADCAST_CONFIG_GET_returns_per_node_results", func(t *testing.T) {
		reply := c.Do("BROADCAST", "CONFIG", "GET", "maxmemory")
		if replyIsError(reply) {
			t.Fatalf("BROADCAST CONFIG GET: unexpected error %s", reply)
		}
		if !strings.HasPrefix(reply, "*") {
			t.Fatalf("BROADCAST CONFIG GET: expected array reply, got %s", reply)
		}
		if !strings.Contains(reply, "maxmemory") {
			t.Errorf("BROADCAST CONFIG GET: expected maxmemory in reply, got %s", reply)
		}
		if !strings.Contains(reply, "127.0.0.1:") {
			t.Errorf("BROADCAST CONFIG GET: expected node addr in reply, got %s", reply)
		}
	})

	t.Run("BROADCAST_CONFIG_SET_propagates_to_all_nodes", func(t *testing.T) {
		reply := c.Do("BROADCAST", "CONFIG", "SET", "hz", "13")
		if replyIsError(reply) {
			t.Fatalf("BROADCAST CONFIG SET: unexpected error %s", reply)
		}
		if !strings.HasPrefix(reply, "*") {
			t.Fatalf("BROADCAST CONFIG SET: expected array reply, got %s", reply)
		}
		if !strings.Contains(reply, "OK") {
			t.Errorf("BROADCAST CONFIG SET: expected OK in each node reply, got %s", reply)
		}
		if !strings.Contains(reply, "127.0.0.1:") {
			t.Errorf("BROADCAST CONFIG SET: expected node addr in reply, got %s", reply)
		}

		for _, master := range sharedCluster.Masters {
			raw := redisCmd(master.Port, "CONFIG", "GET", "hz")
			if !strings.Contains(raw, "13") {
				t.Errorf("node %d: CONFIG GET hz after BROADCAST SET: expected '13', got %s", master.Port, raw)
			}
		}

		_ = c.Do("BROADCAST", "CONFIG", "SET", "hz", "10")
	})

	t.Run("BROADCAST_missing_subcommand_error", func(t *testing.T) {
		reply := c.Do("BROADCAST")
		if !replyIsError(reply) {
			t.Errorf("BROADCAST with no args: expected error, got %s", reply)
		}
	})

	t.Run("BROADCAST_covers_all_masters", func(t *testing.T) {
		reply := c.Do("BROADCAST", "PING")
		if replyIsError(reply) {
			t.Fatalf("BROADCAST PING: unexpected error %s", reply)
		}
		if !strings.HasPrefix(reply, "*") {
			t.Fatalf("BROADCAST PING: expected array reply, got %s", reply)
		}

		for _, master := range sharedCluster.Masters {
			masterAddr := fmt.Sprintf("127.0.0.1:%d", master.Port)
			if !strings.Contains(reply, masterAddr) {
				t.Errorf("BROADCAST PING: reply missing master %s, got: %s", masterAddr, reply)
			}
		}
	})
}
