package integration

import (
	"strconv"
	"testing"
	"time"
)

func TestTTLCommands(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.MustOK(t, "SET", "ttl:key", "value")
	defer c.Do("DEL", "ttl:key")

	reply := c.Do("TTL", "ttl:key")
	if reply != ":-1" {
		t.Errorf("TTL no expiry: expected :-1, got %s", reply)
	}

	reply = c.Do("PTTL", "ttl:key")
	if reply != ":-1" {
		t.Errorf("PTTL no expiry: expected :-1, got %s", reply)
	}

	reply = c.Do("TTL", "ttl:nokey")
	if reply != ":-2" {
		t.Errorf("TTL missing key: expected :-2, got %s", reply)
	}

	reply = c.Do("PTTL", "ttl:nokey")
	if reply != ":-2" {
		t.Errorf("PTTL missing key: expected :-2, got %s", reply)
	}

	c.Do("EXPIRE", "ttl:key", "100")
	reply = c.Do("TTL", "ttl:key")
	n := replyInt(t, reply)
	if n <= 0 || n > 100 {
		t.Errorf("TTL after EXPIRE: expected 1-100, got %d", n)
	}

	reply = c.Do("PTTL", "ttl:key")
	pn := replyInt(t, reply)
	if pn <= 0 || pn > 100000 {
		t.Errorf("PTTL after EXPIRE: expected 1-100000ms, got %d", pn)
	}
}

func TestPersistCommand(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.MustOK(t, "SET", "persist:key", "value")
	defer c.Do("DEL", "persist:key")

	reply := c.Do("EXPIRE", "persist:key", "100")
	if reply != ":1" {
		t.Fatalf("EXPIRE: expected :1, got %s", reply)
	}

	reply = c.Do("TTL", "persist:key")
	n := replyInt(t, reply)
	if n <= 0 {
		t.Fatalf("TTL after EXPIRE: expected positive, got %d", n)
	}

	reply = c.Do("PERSIST", "persist:key")
	if reply != ":1" {
		t.Errorf("PERSIST: expected :1, got %s", reply)
	}

	reply = c.Do("TTL", "persist:key")
	if reply != ":-1" {
		t.Errorf("TTL after PERSIST: expected :-1 (no expiry), got %s", reply)
	}

	reply = c.Do("PERSIST", "persist:nokey")
	if reply != ":0" {
		t.Errorf("PERSIST on missing key: expected :0, got %s", reply)
	}
}

func TestExpireAtCommand(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.MustOK(t, "SET", "expireat:key", "value")
	defer c.Do("DEL", "expireat:key")

	future := time.Now().Add(2 * time.Second).Unix()
	reply := c.Do("EXPIREAT", "expireat:key", strconv.FormatInt(future, 10))
	if reply != ":1" {
		t.Fatalf("EXPIREAT: expected :1, got %s", reply)
	}

	reply = c.Do("TTL", "expireat:key")
	n := replyInt(t, reply)
	if n <= 0 || n > 3 {
		t.Errorf("TTL after EXPIREAT: expected 1-2, got %d", n)
	}

	time.Sleep(2200 * time.Millisecond)
	c.MustNil(t, "expireat:key")
}

func TestPExpireCommand(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.MustOK(t, "SET", "pexpire:key", "value")
	defer c.Do("DEL", "pexpire:key")

	reply := c.Do("PEXPIRE", "pexpire:key", "500")
	if reply != ":1" {
		t.Fatalf("PEXPIRE: expected :1, got %s", reply)
	}

	reply = c.Do("PTTL", "pexpire:key")
	pn := replyInt(t, reply)
	if pn <= 0 || pn > 500 {
		t.Errorf("PTTL after PEXPIRE: expected 1-500, got %d", pn)
	}

	time.Sleep(600 * time.Millisecond)
	c.MustNil(t, "pexpire:key")
}
