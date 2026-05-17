package integration

import (
	"testing"
)

func TestEXISTSCrossSlot(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	keys := []string{"exists:a", "exists:b", "exists:c", "exists:d"}
	for _, k := range keys {
		c.MustOK(t, "SET", k, "v")
	}
	defer func() {
		for _, k := range keys {
			c.Do("DEL", k)
		}
	}()

	args := append([]string{"EXISTS"}, keys...)
	reply := c.Do(args...)
	n := replyInt(t, reply)
	if n != int64(len(keys)) {
		t.Errorf("EXISTS cross-slot: expected %d, got %d", len(keys), n)
	}

	reply = c.Do("EXISTS", "exists:a", "exists:a")
	n = replyInt(t, reply)
	if n != 2 {
		t.Errorf("EXISTS duplicate key: expected 2, got %d", n)
	}

	reply = c.Do("EXISTS", "exists:nothere")
	n = replyInt(t, reply)
	if n != 0 {
		t.Errorf("EXISTS missing: expected 0, got %d", n)
	}
}

func TestUNLINKCrossSlot(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	keys := []string{"unlink:a", "unlink:b", "unlink:c"}
	for _, k := range keys {
		c.MustOK(t, "SET", k, "v")
	}

	args := append([]string{"UNLINK"}, keys...)
	reply := c.Do(args...)
	n := replyInt(t, reply)
	if n != int64(len(keys)) {
		t.Errorf("UNLINK cross-slot: expected %d, got %d", len(keys), n)
	}

	for _, k := range keys {
		c.MustNil(t, k)
	}
}

func TestTOUCHCrossSlot(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	keys := []string{"touch:a", "touch:b", "touch:c"}
	for _, k := range keys {
		c.MustOK(t, "SET", k, "v")
	}
	defer func() {
		for _, k := range keys {
			c.Do("DEL", k)
		}
	}()

	args := append([]string{"TOUCH"}, keys...)
	reply := c.Do(args...)
	n := replyInt(t, reply)
	if n != int64(len(keys)) {
		t.Errorf("TOUCH cross-slot existing: expected %d, got %d", len(keys), n)
	}

	reply = c.Do("TOUCH", "touch:a")
	n = replyInt(t, reply)
	if n != 1 {
		t.Errorf("TOUCH single key: expected 1, got %d", n)
	}

	reply = c.Do("TOUCH", "touch:nothere")
	n = replyInt(t, reply)
	if n != 0 {
		t.Errorf("TOUCH missing key: expected 0, got %d", n)
	}

	reply = c.Do("TOUCH", "touch:a", "touch:nothere", "touch:b")
	n = replyInt(t, reply)
	if n != 2 {
		t.Errorf("TOUCH mixed keys: expected 2, got %d", n)
	}
}

func TestMSETNXCrossSlotReturnsError(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	reply := c.Do("MSETNX", "{slotA}:msnx", "1", "{slotB}:msnx", "2")
	if !replyIsError(reply) {
		t.Errorf("MSETNX cross-slot: expected CROSSSLOT error (atomicity cannot be guaranteed), got %s", reply)
	}
	if !replyIsCrossSlot(reply) {
		t.Errorf("MSETNX cross-slot: expected CROSSSLOT error, got %s", reply)
	}

	c.MustNil(t, "{slotA}:msnx")
	c.MustNil(t, "{slotB}:msnx")
}
