package integration

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

)

func TestMGETKeyOrderPreserved(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	numKeys := 20
	keys := make([]string, numKeys)
	vals := make([]string, numKeys)
	for i := 0; i < numKeys; i++ {
		keys[i] = fmt.Sprintf("mget:order:%d", i)
		vals[i] = fmt.Sprintf("val:%d", i)
		c.MustOK(t, "SET", keys[i], vals[i])
	}
	defer func() {
		for _, k := range keys {
			c.Do("DEL", k)
		}
	}()

	args := append([]string{"MGET"}, keys...)
	c.Send(args...)
	reply := c.ReadReply()

	if !strings.HasPrefix(reply, fmt.Sprintf("*%d:", numKeys)) {
		t.Fatalf("MGET order: expected array of %d, got %s", numKeys, reply)
	}

	inner := reply[len(fmt.Sprintf("*%d:[", numKeys)) : len(reply)-1]
	elems := splitRESPArray(inner, numKeys)
	if len(elems) != numKeys {
		t.Fatalf("MGET order: parsed %d elements, expected %d; reply=%s", len(elems), numKeys, reply)
	}
	for i, elem := range elems {
		got := extractBulkValue(elem)
		if got != vals[i] {
			t.Errorf("MGET order: position %d: expected %q, got %q", i, vals[i], got)
		}
	}
}

func TestMGETKeyOrderWithDuplicates(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.MustOK(t, "SET", "mget:dup:a", "alpha")
	c.MustOK(t, "SET", "mget:dup:b", "beta")
	defer func() {
		c.Do("DEL", "mget:dup:a", "mget:dup:b")
	}()

	c.Send("MGET", "mget:dup:a", "mget:dup:b", "mget:dup:a")
	reply := c.ReadReply()

	if !strings.HasPrefix(reply, "*3:") {
		t.Fatalf("MGET duplicates: expected array of 3, got %s", reply)
	}

	inner := reply[len("*3:[") : len(reply)-1]
	elems := splitRESPArray(inner, 3)
	if len(elems) != 3 {
		t.Fatalf("MGET duplicates: parsed %d elements; reply=%s", len(elems), reply)
	}

	if extractBulkValue(elems[0]) != "alpha" {
		t.Errorf("MGET duplicates: pos 0 expected alpha, got %q", extractBulkValue(elems[0]))
	}
	if extractBulkValue(elems[1]) != "beta" {
		t.Errorf("MGET duplicates: pos 1 expected beta, got %q", extractBulkValue(elems[1]))
	}
	if extractBulkValue(elems[2]) != "alpha" {
		t.Errorf("MGET duplicates: pos 2 expected alpha (duplicate key), got %q", extractBulkValue(elems[2]))
	}
}

func TestMGETMissingKeysPreservePositions(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.MustOK(t, "SET", "mget:miss:exists", "present")
	defer c.Do("DEL", "mget:miss:exists")

	c.Send("MGET", "mget:miss:exists", "mget:miss:nothere1", "mget:miss:exists", "mget:miss:nothere2")
	reply := c.ReadReply()

	if !strings.HasPrefix(reply, "*4:") {
		t.Fatalf("MGET missing keys: expected array of 4, got %s", reply)
	}

	inner := reply[len("*4:[") : len(reply)-1]
	elems := splitRESPArray(inner, 4)
	if len(elems) != 4 {
		t.Fatalf("MGET missing keys: parsed %d elements; reply=%s", len(elems), reply)
	}

	if extractBulkValue(elems[0]) != "present" {
		t.Errorf("MGET missing: pos 0 expected present, got %q", extractBulkValue(elems[0]))
	}
	if elems[1] != "$-1" {
		t.Errorf("MGET missing: pos 1 expected nil ($-1), got %q", elems[1])
	}
	if extractBulkValue(elems[2]) != "present" {
		t.Errorf("MGET missing: pos 2 expected present, got %q", extractBulkValue(elems[2]))
	}
	if elems[3] != "$-1" {
		t.Errorf("MGET missing: pos 3 expected nil ($-1), got %q", elems[3])
	}
}

func TestMSETCrossSlotAllKeysWritten(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	numPairs := 10
	keys := make([]string, numPairs)
	vals := make([]string, numPairs)
	args := []string{"MSET"}
	for i := 0; i < numPairs; i++ {
		keys[i] = fmt.Sprintf("mset:cs:%d", i)
		vals[i] = fmt.Sprintf("msetval:%d", i)
		args = append(args, keys[i], vals[i])
	}
	defer func() {
		for _, k := range keys {
			c.Do("DEL", k)
		}
	}()

	reply := c.Do(args...)
	if reply != "+OK" {
		t.Fatalf("MSET cross-slot: expected +OK, got %s", reply)
	}

	for i, k := range keys {
		c.MustGet(t, k, vals[i])
	}
}

func TestPipelineResponseOrderWithCrossSlotMGET(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	mgetKeys := []string{"mget:pipe:x", "mget:pipe:y", "mget:pipe:z"}
	mgetVals := []string{"vx", "vy", "vz"}
	for i, k := range mgetKeys {
		c.MustOK(t, "SET", k, mgetVals[i])
	}
	c.MustOK(t, "SET", "mget:pipe:after", "after_val")
	defer func() {
		for _, k := range mgetKeys {
			c.Do("DEL", k)
		}
		c.Do("DEL", "mget:pipe:after")
	}()

	c.Send("PING")
	c.Send("MGET", mgetKeys[0], mgetKeys[1], mgetKeys[2])
	c.Send("GET", "mget:pipe:after")
	c.Send("PING")

	r1 := c.ReadReply()
	if r1 != "+PONG" {
		t.Errorf("pipeline order: expected +PONG (cmd 1), got %s", r1)
	}

	r2 := c.ReadReply()
	if !strings.HasPrefix(r2, "*3:") {
		t.Errorf("pipeline order: expected *3:... (MGET, cmd 2), got %s", r2)
	}
	for i, v := range mgetVals {
		if !strings.Contains(r2, v) {
			t.Errorf("pipeline order: MGET reply missing value %d (%s), got %s", i, v, r2)
		}
	}

	r3 := c.ReadReply()
	if extractBulkValue(r3) != "after_val" {
		t.Errorf("pipeline order: expected after_val (GET, cmd 3), got %s", r3)
	}

	r4 := c.ReadReply()
	if r4 != "+PONG" {
		t.Errorf("pipeline order: expected +PONG (cmd 4), got %s", r4)
	}
}

func TestPipelineResponseOrderMixedSlotsMultipleMGET(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	batch1 := []string{"mgpipe:b1:k0", "mgpipe:b1:k1", "mgpipe:b1:k2"}
	batch2 := []string{"mgpipe:b2:k0", "mgpipe:b2:k1"}
	for i, k := range batch1 {
		c.MustOK(t, "SET", k, fmt.Sprintf("b1v%d", i))
	}
	for i, k := range batch2 {
		c.MustOK(t, "SET", k, fmt.Sprintf("b2v%d", i))
	}
	defer func() {
		for _, k := range append(batch1, batch2...) {
			c.Do("DEL", k)
		}
	}()

	c.Send(append([]string{"MGET"}, batch1...)...)
	c.Send(append([]string{"MGET"}, batch2...)...)

	r1 := c.ReadReply()
	r2 := c.ReadReply()

	if !strings.HasPrefix(r1, "*3:") {
		t.Errorf("multi-MGET pipeline: reply 1 expected *3:..., got %s", r1)
	}
	for i := range batch1 {
		if !strings.Contains(r1, fmt.Sprintf("b1v%d", i)) {
			t.Errorf("multi-MGET pipeline: reply 1 missing b1v%d, got %s", i, r1)
		}
	}

	if !strings.HasPrefix(r2, "*2:") {
		t.Errorf("multi-MGET pipeline: reply 2 expected *2:..., got %s", r2)
	}
	for i := range batch2 {
		if !strings.Contains(r2, fmt.Sprintf("b2v%d", i)) {
			t.Errorf("multi-MGET pipeline: reply 2 missing b2v%d, got %s", i, r2)
		}
	}
}

func TestConcurrentClientRequestOrdering(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	numClients := 8
	numRounds := 10
	keysPerMGET := 4

	var wg sync.WaitGroup
	errs := make(chan string, numClients*numRounds*10)

	for clientID := 0; clientID < numClients; clientID++ {
		wg.Add(1)
		go func(cid int) {
			defer wg.Done()
			c := DialProxy(t, proxy)

			mgetKeys := make([]string, keysPerMGET)
			mgetVals := make([]string, keysPerMGET)
			for i := 0; i < keysPerMGET; i++ {
				mgetKeys[i] = fmt.Sprintf("cord:c%d:k%d", cid, i)
				mgetVals[i] = fmt.Sprintf("cv%d_%d", cid, i)
				c.MustOK(t, "SET", mgetKeys[i], mgetVals[i])
			}
			defer func() {
				for _, k := range mgetKeys {
					c.Do("DEL", k)
				}
			}()

			singleKey := fmt.Sprintf("cord:single:c%d", cid)
			singleVal := fmt.Sprintf("sv%d", cid)
			c.MustOK(t, "SET", singleKey, singleVal)
			defer c.Do("DEL", singleKey)

			for round := 0; round < numRounds; round++ {
				c.Send("PING")
				c.Send(append([]string{"MGET"}, mgetKeys...)...)
				c.Send("GET", singleKey)
				c.Send("PING")

				r1 := c.ReadReply()
				if r1 != "+PONG" {
					errs <- fmt.Sprintf("client %d round %d: cmd1 expected +PONG, got %s", cid, round, r1)
					continue
				}

				r2 := c.ReadReply()
				if !strings.HasPrefix(r2, fmt.Sprintf("*%d:", keysPerMGET)) {
					errs <- fmt.Sprintf("client %d round %d: cmd2 expected *%d:... (MGET), got %s", cid, round, keysPerMGET, r2)
					continue
				}
				for _, v := range mgetVals {
					if !strings.Contains(r2, v) {
						errs <- fmt.Sprintf("client %d round %d: MGET missing value %s, got %s", cid, round, v, r2)
					}
				}

				r3 := c.ReadReply()
				if extractBulkValue(r3) != singleVal {
					errs <- fmt.Sprintf("client %d round %d: cmd3 expected %s (GET), got %s", cid, round, singleVal, r3)
				}

				r4 := c.ReadReply()
				if r4 != "+PONG" {
					errs <- fmt.Sprintf("client %d round %d: cmd4 expected +PONG, got %s", cid, round, r4)
				}
			}
		}(clientID)
	}

	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

func TestMGETOrderWithSameSlotKeys(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	keys := []string{"{samesl}:a", "{samesl}:b", "{samesl}:c", "{samesl}:d"}
	vals := []string{"va", "vb", "vc", "vd"}
	for i, k := range keys {
		c.MustOK(t, "SET", k, vals[i])
	}
	defer func() {
		for _, k := range keys {
			c.Do("DEL", k)
		}
	}()

	c.Send("MGET", keys[3], keys[1], keys[0], keys[2])
	reply := c.ReadReply()

	if !strings.HasPrefix(reply, "*4:") {
		t.Fatalf("MGET same-slot order: expected *4:..., got %s", reply)
	}

	inner := reply[len("*4:[") : len(reply)-1]
	elems := splitRESPArray(inner, 4)
	if len(elems) != 4 {
		t.Fatalf("MGET same-slot order: parsed %d elements; reply=%s", len(elems), reply)
	}

	expected := []string{vals[3], vals[1], vals[0], vals[2]}
	for i, exp := range expected {
		if extractBulkValue(elems[i]) != exp {
			t.Errorf("MGET same-slot order: pos %d expected %q, got %q", i, exp, extractBulkValue(elems[i]))
		}
	}
}

func TestMGETReplyOrderAfterAsyncFanOut(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	numKeys := 30
	keys := make([]string, numKeys)
	vals := make([]string, numKeys)
	for i := 0; i < numKeys; i++ {
		keys[i] = fmt.Sprintf("mget:fanout:%02d", i)
		vals[i] = fmt.Sprintf("fanout_val_%02d", i)
		c.MustOK(t, "SET", keys[i], vals[i])
	}
	defer func() {
		for _, k := range keys {
			c.Do("DEL", k)
		}
	}()

	for iter := 0; iter < 5; iter++ {
		args := append([]string{"MGET"}, keys...)
		c.Send(args...)
		reply := c.ReadReply()

		if !strings.HasPrefix(reply, fmt.Sprintf("*%d:", numKeys)) {
			t.Fatalf("iter %d: MGET fanout: expected *%d:..., got %s", iter, numKeys, reply)
		}

		inner := reply[len(fmt.Sprintf("*%d:[", numKeys)) : len(reply)-1]
		elems := splitRESPArray(inner, numKeys)
		if len(elems) != numKeys {
			t.Fatalf("iter %d: MGET fanout: parsed %d elements, expected %d", iter, len(elems), numKeys)
		}

		for i, elem := range elems {
			got := extractBulkValue(elem)
			if got != vals[i] {
				t.Errorf("iter %d: MGET fanout: position %d expected %q, got %q", iter, i, vals[i], got)
			}
		}
	}
}

func TestPipelineResponsePositionForCrossSlotMSET(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	keys := []string{"mset:ppos:a", "mset:ppos:b", "mset:ppos:c"}
	vals := []string{"pp_a", "pp_b", "pp_c"}
	defer func() {
		for _, k := range keys {
			c.Do("DEL", k)
		}
	}()

	msetArgs := []string{"MSET"}
	for i, k := range keys {
		msetArgs = append(msetArgs, k, vals[i])
	}

	c.Send("PING")
	c.Send(msetArgs...)
	c.Send("PING")

	r1 := c.ReadReply()
	if r1 != "+PONG" {
		t.Errorf("pipeline MSET position: cmd1 expected +PONG, got %s", r1)
	}

	r2 := c.ReadReply()
	if r2 != "+OK" {
		t.Errorf("pipeline MSET position: cmd2 (MSET) expected +OK, got %s", r2)
	}

	r3 := c.ReadReply()
	if r3 != "+PONG" {
		t.Errorf("pipeline MSET position: cmd3 expected +PONG, got %s", r3)
	}

	for i, k := range keys {
		c.MustGet(t, k, vals[i])
	}
}

func splitRESPArray(inner string, expectedN int) []string {
	if inner == "" {
		return nil
	}
	elems := make([]string, 0, expectedN)
	depth := 0
	start := 0
	for i := 0; i < len(inner); i++ {
		switch inner[i] {
		case '[':
			depth++
		case ']':
			depth--
		case ',':
			if depth == 0 {
				elems = append(elems, inner[start:i])
				start = i + 1
			}
		}
	}
	if start < len(inner) {
		elems = append(elems, inner[start:])
	}
	return elems
}

func extractBulkValue(reply string) string {
	if !strings.HasPrefix(reply, "$") {
		return reply
	}
	if reply == "$-1" {
		return ""
	}
	idx := strings.Index(reply, ":")
	if idx < 0 {
		return ""
	}
	lenStr := reply[1:idx]
	n, err := strconv.Atoi(lenStr)
	if err != nil || n < 0 {
		return ""
	}
	val := reply[idx+1:]
	if len(val) > n {
		val = val[:n]
	}
	return val
}
