package integration

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPipelineOrderingBasic(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	numCmds := 50
	for i := 0; i < numCmds; i++ {
		c.Send("SET", fmt.Sprintf("{ord}:k%d", i), strconv.Itoa(i))
	}
	for i := 0; i < numCmds; i++ {
		r := c.ReadReply()
		if r != "+OK" {
			t.Errorf("pipeline SET %d: expected +OK, got %s", i, r)
		}
	}

	for i := 0; i < numCmds; i++ {
		c.Send("GET", fmt.Sprintf("{ord}:k%d", i))
	}
	for i := 0; i < numCmds; i++ {
		r := c.ReadReply()
		want := "$" + strconv.Itoa(len(strconv.Itoa(i))) + ":" + strconv.Itoa(i)
		if r != want {
			t.Errorf("pipeline GET %d: expected %s, got %s", i, want, r)
		}
	}

	for i := 0; i < numCmds; i++ {
		c.Do("DEL", fmt.Sprintf("{ord}:k%d", i))
	}
}

func TestPipelineOrderingMultiSlot(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	numCmds := 30
	for i := 0; i < numCmds; i++ {
		key := fmt.Sprintf("ordms:k%d", i)
		c.Send("SET", key, strconv.Itoa(i))
	}
	for i := 0; i < numCmds; i++ {
		r := c.ReadReply()
		if r != "+OK" {
			t.Errorf("multi-slot pipeline SET %d: expected +OK, got %s", i, r)
		}
	}

	for i := 0; i < numCmds; i++ {
		key := fmt.Sprintf("ordms:k%d", i)
		c.Send("GET", key)
	}
	for i := 0; i < numCmds; i++ {
		r := c.ReadReply()
		want := "$" + strconv.Itoa(len(strconv.Itoa(i))) + ":" + strconv.Itoa(i)
		if r != want {
			t.Errorf("multi-slot pipeline GET %d: expected %s, got %s", i, want, r)
		}
	}

	for i := 0; i < numCmds; i++ {
		c.Do("DEL", fmt.Sprintf("ordms:k%d", i))
	}
}

func TestPipelineOrderingMixedLocalAndBackend(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.MustOK(t, "SET", "{mixord}:val", "hello")
	defer c.Do("DEL", "{mixord}:val")

	c.Send("PING")
	c.Send("GET", "{mixord}:val")
	c.Send("PING")
	c.Send("GET", "{mixord}:val")
	c.Send("ECHO", "world")
	c.Send("GET", "{mixord}:val")

	replies := []string{
		c.ReadReply(),
		c.ReadReply(),
		c.ReadReply(),
		c.ReadReply(),
		c.ReadReply(),
		c.ReadReply(),
	}

	if replies[0] != "+PONG" {
		t.Errorf("reply[0]: expected +PONG, got %s", replies[0])
	}
	if extractBulkValue(replies[1]) != "hello" {
		t.Errorf("reply[1]: expected hello, got %s", replies[1])
	}
	if replies[2] != "+PONG" {
		t.Errorf("reply[2]: expected +PONG, got %s", replies[2])
	}
	if extractBulkValue(replies[3]) != "hello" {
		t.Errorf("reply[3]: expected hello, got %s", replies[3])
	}
	if extractBulkValue(replies[4]) != "world" {
		t.Errorf("reply[4]: expected world, got %s", replies[4])
	}
	if extractBulkValue(replies[5]) != "hello" {
		t.Errorf("reply[5]: expected hello, got %s", replies[5])
	}
}

func TestPipelineOrderingWithWATCH(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.MustOK(t, "SET", "{watch_ord}:key", "initial")
	defer c.Do("DEL", "{watch_ord}:key")

	c.Send("SET", "{watch_ord}:before", "before_val")
	c.Send("WATCH", "{watch_ord}:key")
	c.Send("GET", "{watch_ord}:before")

	r1 := c.ReadReply()
	if r1 != "+OK" {
		t.Errorf("ordering WATCH: SET before expected +OK, got %s", r1)
	}

	r2 := c.ReadReply()
	if r2 != "+OK" {
		t.Errorf("ordering WATCH: WATCH expected +OK, got %s", r2)
	}

	r3 := c.ReadReply()
	if extractBulkValue(r3) != "before_val" {
		t.Errorf("ordering WATCH: GET after WATCH expected before_val, got %s", r3)
	}

	c.Do("DEL", "{watch_ord}:before")
}

func TestPipelineOrderingWATCHMultiExec(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.MustOK(t, "SET", "{wme}:counter", "0")
	defer c.Do("DEL", "{wme}:counter")
	defer c.Do("DEL", "{wme}:before")
	defer c.Do("DEL", "{wme}:after")

	c.Send("SET", "{wme}:before", "before")
	c.Send("WATCH", "{wme}:counter")
	c.Send("MULTI")
	c.Send("INCR", "{wme}:counter")
	c.Send("EXEC")
	c.Send("SET", "{wme}:after", "after")

	r1 := c.ReadReply()
	if r1 != "+OK" {
		t.Errorf("WME ordering: SET before expected +OK, got %s", r1)
	}

	r2 := c.ReadReply()
	if r2 != "+OK" {
		t.Errorf("WME ordering: WATCH expected +OK, got %s", r2)
	}

	r3 := c.ReadReply()
	if r3 != "+OK" {
		t.Errorf("WME ordering: MULTI expected +OK, got %s", r3)
	}

	r4 := c.ReadReply()
	if r4 != "+QUEUED" {
		t.Errorf("WME ordering: INCR in MULTI expected +QUEUED, got %s", r4)
	}

	r5 := c.ReadReply()
	if !strings.HasPrefix(r5, "*1:") {
		t.Errorf("WME ordering: EXEC expected *1:..., got %s", r5)
	}

	r6 := c.ReadReply()
	if r6 != "+OK" {
		t.Errorf("WME ordering: SET after expected +OK, got %s", r6)
	}

	c.MustGet(t, "{wme}:counter", "1")
}

func TestPipelineOrderingHighConcurrency(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	numClients := 20
	numOps := 50
	var wg sync.WaitGroup
	errs := make(chan string, numClients*numOps*2)

	for clientID := 0; clientID < numClients; clientID++ {
		wg.Add(1)
		go func(cid int) {
			defer wg.Done()
			c := DialProxy(t, proxy)

			for j := 0; j < numOps; j++ {
				key := fmt.Sprintf("hcord:c%d:k%d", cid, j)
				c.Send("SET", key, fmt.Sprintf("v%d_%d", cid, j))
			}
			for j := 0; j < numOps; j++ {
				r := c.ReadReply()
				if r != "+OK" {
					errs <- fmt.Sprintf("client %d SET %d: expected +OK, got %s", cid, j, r)
				}
			}

			for j := 0; j < numOps; j++ {
				key := fmt.Sprintf("hcord:c%d:k%d", cid, j)
				c.Send("GET", key)
			}
			for j := 0; j < numOps; j++ {
				r := c.ReadReply()
				want := fmt.Sprintf("v%d_%d", cid, j)
				if extractBulkValue(r) != want {
					errs <- fmt.Sprintf("client %d GET %d: expected %s, got %s", cid, j, want, r)
				}
			}

			for j := 0; j < numOps; j++ {
				c.Do("DEL", fmt.Sprintf("hcord:c%d:k%d", cid, j))
			}
		}(clientID)
	}

	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

func TestPipelineOrderingInterleavedPingAndGet(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	numKeys := 20
	for i := 0; i < numKeys; i++ {
		c.MustOK(t, "SET", fmt.Sprintf("{interleave}:k%d", i), strconv.Itoa(i*10))
	}
	defer func() {
		for i := 0; i < numKeys; i++ {
			c.Do("DEL", fmt.Sprintf("{interleave}:k%d", i))
		}
	}()

	for i := 0; i < numKeys; i++ {
		c.Send("GET", fmt.Sprintf("{interleave}:k%d", i))
		c.Send("PING")
	}

	for i := 0; i < numKeys; i++ {
		rGet := c.ReadReply()
		want := strconv.Itoa(i * 10)
		if extractBulkValue(rGet) != want {
			t.Errorf("interleaved GET %d: expected %s, got %s", i, want, rGet)
		}

		rPing := c.ReadReply()
		if rPing != "+PONG" {
			t.Errorf("interleaved PING %d: expected +PONG, got %s", i, rPing)
		}
	}
}

func TestPipelineOrderingStressMultipleSlots(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	numClients := 10
	numRounds := 20
	numKeys := 15

	var wg sync.WaitGroup
	errs := make(chan string, numClients*numRounds*numKeys)

	for cid := 0; cid < numClients; cid++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			c := DialProxy(t, proxy)

			keys := make([]string, numKeys)
			vals := make([]string, numKeys)
			for i := 0; i < numKeys; i++ {
				keys[i] = fmt.Sprintf("stress:c%d:k%d", clientID, i)
				vals[i] = fmt.Sprintf("val_%d_%d_%d", clientID, i, time.Now().UnixNano())
				c.MustOK(t, "SET", keys[i], vals[i])
			}
			defer func() {
				for _, k := range keys {
					c.Do("DEL", k)
				}
			}()

			for round := 0; round < numRounds; round++ {
				for i := 0; i < numKeys; i++ {
					c.Send("GET", keys[i])
				}
				for i := 0; i < numKeys; i++ {
					r := c.ReadReply()
					if extractBulkValue(r) != vals[i] {
						errs <- fmt.Sprintf("client %d round %d key %d: expected %s, got %s", clientID, round, i, vals[i], r)
					}
				}
			}
		}(cid)
	}

	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

func TestPipelineOrderingMULTIEXECInPipeline(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	defer func() {
		c.Do("DEL", "{txord}:before", "{txord}:after")
	}()

	c.Send("SET", "{txord}:before", "bval")
	c.Send("MULTI")
	c.Send("SET", "{txord}:a", "10")
	c.Send("SET", "{txord}:b", "20")
	c.Send("EXEC")
	c.Send("SET", "{txord}:after", "aval")

	r1 := c.ReadReply()
	if r1 != "+OK" {
		t.Errorf("MULTI pipeline: SET before expected +OK, got %s", r1)
	}

	r2 := c.ReadReply()
	if r2 != "+OK" {
		t.Errorf("MULTI pipeline: MULTI expected +OK, got %s", r2)
	}

	r3 := c.ReadReply()
	if r3 != "+QUEUED" {
		t.Errorf("MULTI pipeline: SET a expected +QUEUED, got %s", r3)
	}

	r4 := c.ReadReply()
	if r4 != "+QUEUED" {
		t.Errorf("MULTI pipeline: SET b expected +QUEUED, got %s", r4)
	}

	r5 := c.ReadReply()
	if !strings.HasPrefix(r5, "*2:") {
		t.Errorf("MULTI pipeline: EXEC expected *2:..., got %s", r5)
	}

	r6 := c.ReadReply()
	if r6 != "+OK" {
		t.Errorf("MULTI pipeline: SET after expected +OK, got %s", r6)
	}

	c.MustGet(t, "{txord}:a", "10")
	c.MustGet(t, "{txord}:b", "20")
	c.Do("DEL", "{txord}:a", "{txord}:b")
}

func TestPipelineOrderingRespQueueGrowth(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	numKeys := 200
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("rqgrow:k%d", i)
		c.Send("SET", key, strconv.Itoa(i))
	}
	for i := 0; i < numKeys; i++ {
		r := c.ReadReply()
		if r != "+OK" {
			t.Errorf("respQueue growth SET %d: expected +OK, got %s", i, r)
		}
	}

	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("rqgrow:k%d", i)
		c.Send("GET", key)
	}
	for i := 0; i < numKeys; i++ {
		r := c.ReadReply()
		want := strconv.Itoa(i)
		if extractBulkValue(r) != want {
			t.Errorf("respQueue growth GET %d: expected %s, got %s", i, want, r)
		}
	}

	for i := 0; i < numKeys; i++ {
		c.Do("DEL", fmt.Sprintf("rqgrow:k%d", i))
	}
}

func TestPipelineOrderingConcurrentSingleConnection(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	numIter := 100
	for iter := 0; iter < numIter; iter++ {
		key := fmt.Sprintf("singleconn:iter%d", iter)
		val := fmt.Sprintf("val%d", iter)

		c.Send("SET", key, val)
		c.Send("GET", key)
		c.Send("DEL", key)

		r1 := c.ReadReply()
		if r1 != "+OK" {
			t.Errorf("iter %d: SET expected +OK, got %s", iter, r1)
		}
		r2 := c.ReadReply()
		if extractBulkValue(r2) != val {
			t.Errorf("iter %d: GET expected %s, got %s", iter, val, r2)
		}
		r3 := c.ReadReply()
		if r3 != ":1" {
			t.Errorf("iter %d: DEL expected :1, got %s", iter, r3)
		}
	}
}
