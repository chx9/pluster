package integration

import (
	"strings"
	"testing"
)

func TestMultiExecBasic(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.Do("DEL", "{tx}:a", "{tx}:b")

	if r := c.Do("MULTI"); r != "+OK" {
		t.Fatalf("MULTI: expected +OK, got %s", r)
	}
	if r := c.Do("SET", "{tx}:a", "1"); r != "+QUEUED" {
		t.Fatalf("SET in MULTI: expected +QUEUED, got %s", r)
	}
	if r := c.Do("SET", "{tx}:b", "2"); r != "+QUEUED" {
		t.Fatalf("SET in MULTI: expected +QUEUED, got %s", r)
	}
	if r := c.Do("GET", "{tx}:a"); r != "+QUEUED" {
		t.Fatalf("GET in MULTI: expected +QUEUED, got %s", r)
	}

	reply := c.Do("EXEC")
	if !strings.HasPrefix(reply, "*3:") {
		t.Fatalf("EXEC: expected 3-element array, got %s", reply)
	}
	if !strings.Contains(reply, "+OK") {
		t.Errorf("EXEC: expected +OK in results, got %s", reply)
	}
	if !strings.Contains(reply, "$1:1") {
		t.Errorf("EXEC: expected GET result '1', got %s", reply)
	}

	c.Do("DEL", "{tx}:a", "{tx}:b")
}

func TestMultiExecEmptyQueue(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.Do("MULTI")
	reply := c.Do("EXEC")
	if reply != "*0:[]" {
		t.Fatalf("EXEC empty: expected *0:[], got %s", reply)
	}
}

func TestMultiDiscardRollback(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.Do("SET", "{tx}:discard", "original")
	c.Do("MULTI")
	c.Do("SET", "{tx}:discard", "changed")
	if r := c.Do("DISCARD"); r != "+OK" {
		t.Fatalf("DISCARD: expected +OK, got %s", r)
	}
	c.MustGet(t, "{tx}:discard", "original")
	c.Do("DEL", "{tx}:discard")
}

func TestMultiExecAbortOnError(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.Do("MULTI")
	if r := c.Do("SET", "{tx}:k", "v"); r != "+QUEUED" {
		t.Fatalf("SET: expected +QUEUED, got %s", r)
	}
	if r := c.Do("UNKNOWNCMD", "x"); !strings.HasPrefix(r, "-ERR") {
		t.Fatalf("unknown cmd in MULTI: expected -ERR, got %s", r)
	}
	reply := c.Do("EXEC")
	if !strings.HasPrefix(reply, "-EXECABORT") {
		t.Fatalf("EXEC after error: expected -EXECABORT, got %s", reply)
	}
}

func TestMultiNestedError(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.Do("MULTI")
	if r := c.Do("MULTI"); !strings.HasPrefix(r, "-ERR") {
		t.Fatalf("nested MULTI: expected -ERR, got %s", r)
	}
	c.Do("DISCARD")
}

func TestMultiWatchInsideError(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.Do("MULTI")
	if r := c.Do("WATCH", "{tx}:k"); !strings.HasPrefix(r, "-ERR") {
		t.Fatalf("WATCH inside MULTI: expected -ERR, got %s", r)
	}
	c.Do("DISCARD")
}

func TestMultiExecCrossSlotError(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.Do("MULTI")
	c.Do("SET", "key1", "v1")
	c.Do("SET", "key2", "v2")
	reply := c.Do("EXEC")
	if !strings.HasPrefix(reply, "-CROSSSLOT") {
		t.Fatalf("EXEC cross-slot: expected -CROSSSLOT, got %s", reply)
	}
}

func TestMultiExecSameHashTag(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.Do("DEL", "{user}:name", "{user}:age")

	c.Do("MULTI")
	c.Do("SET", "{user}:name", "alice")
	c.Do("SET", "{user}:age", "30")
	c.Do("GET", "{user}:name")
	reply := c.Do("EXEC")

	if !strings.HasPrefix(reply, "*3:") {
		t.Fatalf("EXEC same-slot: expected 3-element array, got %s", reply)
	}
	if !strings.Contains(reply, "alice") {
		t.Errorf("EXEC same-slot: expected 'alice' in result, got %s", reply)
	}

	c.MustGet(t, "{user}:name", "alice")
	c.MustGet(t, "{user}:age", "30")
	c.Do("DEL", "{user}:name", "{user}:age")
}

func TestMultiWatchExecSuccess(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.Do("SET", "{watch}:k", "0")

	if r := c.Do("WATCH", "{watch}:k"); r != "+OK" {
		t.Fatalf("WATCH: expected +OK, got %s", r)
	}
	c.Do("MULTI")
	c.Do("INCR", "{watch}:k")
	reply := c.Do("EXEC")

	if !strings.HasPrefix(reply, "*1:") {
		t.Fatalf("WATCH+EXEC success: expected 1-element array, got %s", reply)
	}
	if !strings.Contains(reply, ":1") {
		t.Errorf("WATCH+EXEC success: expected INCR result 1, got %s", reply)
	}
	c.Do("DEL", "{watch}:k")
}

func TestMultiWatchExecAbortOnModified(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c1 := DialProxy(t, proxy)
	c2 := DialProxy(t, proxy)

	c1.Do("SET", "{watch}:mod", "original")

	if r := c1.Do("WATCH", "{watch}:mod"); r != "+OK" {
		t.Fatalf("WATCH: expected +OK, got %s", r)
	}

	c2.Do("SET", "{watch}:mod", "modified")

	c1.Do("MULTI")
	c1.Do("SET", "{watch}:mod", "from-tx")
	reply := c1.Do("EXEC")

	if reply != "*-1" {
		t.Fatalf("WATCH+EXEC aborted: expected *-1 (nil), got %s", reply)
	}
	c1.MustGet(t, "{watch}:mod", "modified")
	c1.Do("DEL", "{watch}:mod")
}

func TestMultiUnwatch(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c1 := DialProxy(t, proxy)
	c2 := DialProxy(t, proxy)

	c1.Do("SET", "{watch}:unw", "original")
	c1.Do("WATCH", "{watch}:unw")

	c2.Do("SET", "{watch}:unw", "modified")

	if r := c1.Do("UNWATCH"); r != "+OK" {
		t.Fatalf("UNWATCH: expected +OK, got %s", r)
	}

	c1.Do("MULTI")
	c1.Do("SET", "{watch}:unw", "from-tx")
	reply := c1.Do("EXEC")

	if !strings.HasPrefix(reply, "*1:") {
		t.Fatalf("UNWATCH+EXEC: expected 1-element array (not aborted), got %s", reply)
	}
	c1.MustGet(t, "{watch}:unw", "from-tx")
	c1.Do("DEL", "{watch}:unw")
}

func TestMultiReset(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.Do("SET", "{tx}:reset", "original")
	c.Do("MULTI")
	c.Do("SET", "{tx}:reset", "changed")
	if r := c.Do("RESET"); r != "+RESET" {
		t.Fatalf("RESET in MULTI: expected +RESET, got %s", r)
	}
	c.MustGet(t, "{tx}:reset", "original")
	c.Do("DEL", "{tx}:reset")
}

func TestMultiExecAfterDiscard(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	if r := c.Do("EXEC"); !strings.HasPrefix(r, "-ERR") {
		t.Fatalf("EXEC without MULTI: expected -ERR, got %s", r)
	}
	if r := c.Do("DISCARD"); !strings.HasPrefix(r, "-ERR") {
		t.Fatalf("DISCARD without MULTI: expected -ERR, got %s", r)
	}
}
