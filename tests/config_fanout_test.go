package integration

import (
	"strings"
	"testing"
)

func TestConfigFanout(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	t.Run("CONFIG_GET_maxmemory_returns_array", func(t *testing.T) {
		reply := c.Do("CONFIG", "GET", "maxmemory")
		if replyIsError(reply) {
			t.Fatalf("CONFIG GET maxmemory: unexpected error %s", reply)
		}
		if !strings.HasPrefix(reply, "*") {
			t.Fatalf("CONFIG GET maxmemory: expected array reply, got %s", reply)
		}
		if !strings.Contains(reply, "maxmemory") {
			t.Errorf("CONFIG GET maxmemory: expected 'maxmemory' in reply, got %s", reply)
		}
	})

	t.Run("CONFIG_SET_and_GET_roundtrip", func(t *testing.T) {
		reply := c.Do("CONFIG", "SET", "hz", "15")
		if reply != "+OK" {
			t.Fatalf("CONFIG SET hz 15: expected +OK, got %s", reply)
		}

		reply = c.Do("CONFIG", "GET", "hz")
		if replyIsError(reply) {
			t.Fatalf("CONFIG GET hz: unexpected error %s", reply)
		}
		if !strings.Contains(reply, "15") {
			t.Errorf("CONFIG GET hz after SET: expected '15' in reply, got %s", reply)
		}
		if !strings.Contains(reply, "hz") {
			t.Errorf("CONFIG GET hz after SET: expected 'hz' in reply, got %s", reply)
		}

		_ = c.Do("CONFIG", "SET", "hz", "10")
	})

	t.Run("CONFIG_RESETSTAT_ok", func(t *testing.T) {
		reply := c.Do("CONFIG", "RESETSTAT")
		if reply != "+OK" {
			t.Errorf("CONFIG RESETSTAT: expected +OK, got %s", reply)
		}
	})

	t.Run("CONFIG_GET_wildcard", func(t *testing.T) {
		reply := c.Do("CONFIG", "GET", "save")
		if replyIsError(reply) {
			t.Fatalf("CONFIG GET save: unexpected error %s", reply)
		}
		if !strings.HasPrefix(reply, "*") {
			t.Fatalf("CONFIG GET save: expected array reply, got %s", reply)
		}
	})

	t.Run("CONFIG_SET_propagates_to_all_nodes", func(t *testing.T) {
		reply := c.Do("CONFIG", "SET", "hz", "17")
		if reply != "+OK" {
			t.Fatalf("CONFIG SET hz 17: expected +OK, got %s", reply)
		}

		for _, master := range sharedCluster.Masters {
			raw := redisCmd(master.Port, "CONFIG", "GET", "hz")
			if !strings.Contains(raw, "17") {
				t.Errorf("node %d: CONFIG GET hz after proxy SET: expected '17', got %s", master.Port, raw)
			}
		}

		_ = c.Do("CONFIG", "SET", "hz", "10")
	})
}
