package integration

// Tests for the backendMux fan-in path used by cross-slot MGET/MSET/DEL/EXISTS/TOUCH.
// These complement crossslot_fanout_test.go and multikey_ordering_test.go by targeting
// scenarios specific to the in-event-loop fan-in implementation:
//
//   - Same-slot fast path (no fan-in allocation)
//   - Correct values across all 5 supported commands
//   - nil entries preserved at correct positions
//   - Integer merge (DEL/EXISTS) sums across slots
//   - Pipeline ordering: fan-in responses don't displace adjacent commands
//   - Concurrent clients each get independent, correct results
//   - Large fan-out (many distinct slots)

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// TestMGETSameSlotFastPath: all keys share one slot → proxy sends the request
// unchanged without allocating a fanInState.
func TestMGETSameSlotFastPath(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.MustOK(t, "SET", "{fs}:a", "1")
	c.MustOK(t, "SET", "{fs}:b", "2")
	c.MustOK(t, "SET", "{fs}:c", "3")
	defer c.Do("DEL", "{fs}:a", "{fs}:b", "{fs}:c")

	reply := c.Do("MGET", "{fs}:a", "{fs}:b", "{fs}:c")
	elems := parseMGETReply(t, reply, 3)
	assertBulk(t, "pos0", elems[0], "1")
	assertBulk(t, "pos1", elems[1], "2")
	assertBulk(t, "pos2", elems[2], "3")
}

// TestMGETCrossSlotCorrectValues: values come back for each key regardless of
// which Redis node owns the slot.
func TestMGETCrossSlotCorrectValues(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	keys := []string{"mfi:k1", "mfi:k2", "mfi:k3", "mfi:k4", "mfi:k5"}
	for i, k := range keys {
		c.MustOK(t, "SET", k, strconv.Itoa(i+1))
	}
	defer func() {
		for _, k := range keys {
			c.Do("DEL", k)
		}
	}()

	args := append([]string{"MGET"}, keys...)
	reply := c.Do(args...)
	elems := parseMGETReply(t, reply, len(keys))
	for i, want := range []string{"1", "2", "3", "4", "5"} {
		assertBulk(t, fmt.Sprintf("pos%d", i), elems[i], want)
	}
}

// TestMGETCrossSlotNilPositions: nil entries appear at the correct index.
func TestMGETCrossSlotNilPositions(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.MustOK(t, "SET", "mfi:nil:a", "here")
	c.Do("DEL", "mfi:nil:missing")
	defer c.Do("DEL", "mfi:nil:a")

	// key order: present, missing, present
	reply := c.Do("MGET", "mfi:nil:a", "mfi:nil:missing", "mfi:nil:a")
	elems := parseMGETReply(t, reply, 3)
	assertBulk(t, "pos0", elems[0], "here")
	if elems[1] != "$-1" {
		t.Errorf("pos1: want nil ($-1), got %q", elems[1])
	}
	assertBulk(t, "pos2", elems[2], "here")
}

// TestMSETCrossSlotAllSlotsWritten: every key is persisted after a cross-slot MSET.
func TestMSETCrossSlotAllSlotsWritten(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	keys := []string{"mfi:ms:1", "mfi:ms:2", "mfi:ms:3", "mfi:ms:4"}
	defer func() {
		for _, k := range keys {
			c.Do("DEL", k)
		}
	}()

	args := []string{"MSET"}
	for i, k := range keys {
		args = append(args, k, strconv.Itoa(i*10))
	}
	c.MustOK(t, args...)

	for i, k := range keys {
		c.MustGet(t, k, strconv.Itoa(i*10))
	}
}

// TestDELCrossSlotSum: integer reply equals total deleted keys across all slots.
func TestDELCrossSlotSum(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	present := []string{"mfi:del:a", "mfi:del:b", "mfi:del:c"}
	for _, k := range present {
		c.MustOK(t, "SET", k, "v")
	}

	// 3 present + 1 absent → should return 3
	reply := c.Do("DEL", "mfi:del:a", "mfi:del:b", "mfi:del:c", "mfi:del:absent")
	if n := replyInt(t, reply); n != 3 {
		t.Errorf("DEL cross-slot: want 3, got %d", n)
	}
	for _, k := range present {
		c.MustNil(t, k)
	}
}

// TestEXISTSCrossSlotSum: integer reply counts all matching keys (duplicates included).
func TestEXISTSCrossSlotSum(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	keys := []string{"mfi:ex:1", "mfi:ex:2", "mfi:ex:3"}
	for _, k := range keys {
		c.MustOK(t, "SET", k, "v")
	}
	defer func() {
		for _, k := range keys {
			c.Do("DEL", k)
		}
	}()

	reply := c.Do("EXISTS", "mfi:ex:1", "mfi:ex:2", "mfi:ex:3", "mfi:ex:absent")
	if n := replyInt(t, reply); n != 3 {
		t.Errorf("EXISTS cross-slot: want 3, got %d", n)
	}
	// duplicate key counts twice
	reply = c.Do("EXISTS", "mfi:ex:1", "mfi:ex:1", "mfi:ex:absent")
	if n := replyInt(t, reply); n != 2 {
		t.Errorf("EXISTS duplicate: want 2, got %d", n)
	}
}

// TestPipelineFanInDoesNotDisruptOrder: pipelining SET · MGET(cross) · GET · MSET(cross) · GET
// must deliver responses in submission order.
func TestPipelineFanInDoesNotDisruptOrder(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.MustOK(t, "SET", "mfi:p:a", "alpha")
	c.MustOK(t, "SET", "mfi:p:b", "beta")
	c.MustOK(t, "SET", "mfi:p:c", "gamma")
	defer c.Do("DEL", "mfi:p:a", "mfi:p:b", "mfi:p:c", "mfi:p:x")

	c.Send("SET", "mfi:p:x", "extra")
	c.Send("MGET", "mfi:p:a", "mfi:p:b", "mfi:p:c")
	c.Send("GET", "mfi:p:x")
	c.Send("MSET", "mfi:p:a", "A", "mfi:p:b", "B")
	c.Send("GET", "mfi:p:a")

	// reply 1: SET
	if r := c.ReadReply(); r != "+OK" {
		t.Fatalf("reply1 (SET): want +OK got %q", r)
	}
	// reply 2: MGET — 3-element array in order
	mget := c.ReadReply()
	elems := parseMGETReply(t, mget, 3)
	assertBulk(t, "mget[0]", elems[0], "alpha")
	assertBulk(t, "mget[1]", elems[1], "beta")
	assertBulk(t, "mget[2]", elems[2], "gamma")
	// reply 3: GET x
	if r := c.ReadReply(); extractBulkValue(r) != "extra" {
		t.Errorf("reply3 (GET x): want 'extra' got %q", r)
	}
	// reply 4: MSET
	if r := c.ReadReply(); r != "+OK" {
		t.Errorf("reply4 (MSET): want +OK got %q", r)
	}
	// reply 5: GET a — must reflect the MSET above
	if r := c.ReadReply(); extractBulkValue(r) != "A" {
		t.Errorf("reply5 (GET a): want 'A' got %q", r)
	}
}

// TestConcurrentFanInClients: 20 concurrent clients each issue repeated MGET
// against their own keys; no cross-contamination between clients.
func TestConcurrentFanInClients(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	const nClients = 20
	const nOps = 50

	setup := DialProxy(t, proxy)
	for id := 0; id < nClients; id++ {
		for i := 0; i < 3; i++ {
			k := fmt.Sprintf("mfi:cc:%d:%d", id, i)
			v := fmt.Sprintf("%d_%d", id, i)
			setup.MustOK(t, "SET", k, v)
		}
	}
	defer func() {
		for id := 0; id < nClients; id++ {
			for i := 0; i < 3; i++ {
				setup.Do("DEL", fmt.Sprintf("mfi:cc:%d:%d", id, i))
			}
		}
	}()

	errs := make([]string, nClients)
	var wg sync.WaitGroup
	for id := 0; id < nClients; id++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			c := DialProxy(t, proxy)
			keys := []string{
				fmt.Sprintf("mfi:cc:%d:0", id),
				fmt.Sprintf("mfi:cc:%d:1", id),
				fmt.Sprintf("mfi:cc:%d:2", id),
			}
			for iter := 0; iter < nOps; iter++ {
				args := append([]string{"MGET"}, keys...)
				reply := c.Do(args...)
				elems := parseMGETReply(t, reply, 3)
				for i, elem := range elems {
					want := fmt.Sprintf("%d_%d", id, i)
					if extractBulkValue(elem) != want {
						errs[id] = fmt.Sprintf("client %d iter %d pos %d: want %q got %q",
							id, iter, i, want, elem)
						return
					}
				}
			}
		}(id)
	}
	wg.Wait()

	for id, e := range errs {
		if e != "" {
			t.Error(e)
			_ = id
		}
	}
}

// TestMGETLargeFanOut: 20 keys spread across many slots, all values correct.
func TestMGETLargeFanOut(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	const n = 20
	keys := make([]string, n)
	for i := range keys {
		keys[i] = fmt.Sprintf("mfi:large:%d", i)
		c.MustOK(t, "SET", keys[i], fmt.Sprintf("v%d", i))
	}
	defer func() {
		for _, k := range keys {
			c.Do("DEL", k)
		}
	}()

	args := append([]string{"MGET"}, keys...)
	reply := c.Do(args...)
	elems := parseMGETReply(t, reply, n)
	for i, elem := range elems {
		want := fmt.Sprintf("v%d", i)
		if extractBulkValue(elem) != want {
			t.Errorf("large fanout pos %d: want %q got %q", i, want, elem)
		}
	}
}

// ---------- local helpers ----------

// parseMGETReply splits a ReadReply array string into its elements.
// The format is "*N:[e1,e2,...,eN]".
func parseMGETReply(t *testing.T, reply string, n int) []string {
	t.Helper()
	prefix := fmt.Sprintf("*%d:[", n)
	if !strings.HasPrefix(reply, prefix) || !strings.HasSuffix(reply, "]") {
		t.Fatalf("parseMGETReply: expected *%d:[...], got %q", n, reply)
	}
	inner := reply[len(prefix) : len(reply)-1]
	return splitRESPArray(inner, n)
}

// assertBulk checks a single element from parseMGETReply equals "$N:value".
func assertBulk(t *testing.T, label, elem, want string) {
	t.Helper()
	if extractBulkValue(elem) != want {
		t.Errorf("%s: want %q got %q (raw: %q)", label, want, extractBulkValue(elem), elem)
	}
}
