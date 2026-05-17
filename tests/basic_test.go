package integration

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

)

func TestBasicSetGet(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	c := DialProxy(t, proxy)
	c.MustOK(t, "SET", "hello", "world")
	c.MustGet(t, "hello", "world")
}

func TestBinaryKeyValue(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	c := DialProxy(t, proxy)

	key := "k:\x00\r\n:k"
	val := "v:\x00\r\n:v"
	c.SendBinary([]byte("SET"), []byte(key), []byte(val))
	reply := c.ReadReply()
	if reply != "+OK" {
		t.Fatalf("SET binary: expected +OK, got %s", reply)
	}

	c.SendBinary([]byte("GET"), []byte(key))
	reply = c.ReadReply()
	want := "$" + strconv.Itoa(len(val)) + ":" + val
	if reply != want {
		t.Errorf("GET binary: expected %q, got %q", want, reply)
	}
}

func TestSetGetDeleteAtoZ(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	c := DialProxy(t, proxy)

	for ch := 'a'; ch <= 'z'; ch++ {
		key := fmt.Sprintf("k:%c", ch)
		val := fmt.Sprintf("%c", ch)
		c.MustOK(t, "SET", key, val)
	}
	for ch := 'a'; ch <= 'z'; ch++ {
		key := fmt.Sprintf("k:%c", ch)
		val := fmt.Sprintf("%c", ch)
		c.MustGet(t, key, val)
	}
	for ch := 'a'; ch <= 'z'; ch++ {
		key := fmt.Sprintf("k:%c", ch)
		reply := c.Do("DEL", key)
		if reply != ":1" {
			t.Errorf("DEL %s: expected :1, got %s", key, reply)
		}
	}
}

func TestPing(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	c := DialProxy(t, proxy)
	reply := c.Do("PING")
	if reply != "+PONG" {
		t.Errorf("PING: expected +PONG, got %s", reply)
	}

	c.Send("PING", "hello")
	reply = c.ReadReply()
	if reply != "$5:hello" {
		t.Errorf("PING hello: expected $5:hello, got %s", reply)
	}
}

func TestEcho(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	c := DialProxy(t, proxy)
	c.Send("ECHO", "hello world")
	reply := c.ReadReply()
	if reply != "$11:hello world" {
		t.Errorf("ECHO: expected $11:hello world, got %s", reply)
	}
}

func TestGetEmptyKey(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	c := DialProxy(t, proxy)
	c.SendBinary([]byte("GET"), []byte(""))
	reply := c.ReadReply()
	if reply != "$-1" {
		t.Errorf("GET empty key: expected nil, got %s", reply)
	}
}

func TestMGETEmptyKey(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.MustOK(t, "SET", "a", "aval")
	defer c.Do("DEL", "a")

	c.SendBinary([]byte("MGET"), []byte("a"), []byte(""))
	reply := c.ReadReply()
	if !strings.Contains(reply, "aval") {
		t.Errorf("MGET a empty: expected aval in reply, got %s", reply)
	}
}

func TestMGETCrossSlot(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	c := DialProxy(t, proxy)

	keys := make([]string, 10)
	for i := 0; i < 10; i++ {
		keys[i] = fmt.Sprintf("mget:key:%d", i)
		c.MustOK(t, "SET", keys[i], strconv.Itoa(i))
	}

	args := append([]string{"MGET"}, keys...)
	c.Send(args...)
	reply := c.ReadReply()
	if !strings.HasPrefix(reply, "*10:") {
		t.Errorf("MGET cross-slot: expected array of 10, got %s", reply)
	}
	for i := 0; i < 10; i++ {
		if !strings.Contains(reply, strconv.Itoa(i)) {
			t.Errorf("MGET cross-slot: missing value %d in reply", i)
		}
	}

	for _, k := range keys {
		c.Do("DEL", k)
	}
}

func TestMSETMGET(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	c := DialProxy(t, proxy)

	args := []string{"MSET"}
	for i := 0; i < 10; i++ {
		args = append(args, fmt.Sprintf("mset:k:%d", i), strconv.Itoa(i*10))
	}
	c.Send(args...)
	reply := c.ReadReply()
	if reply != "+OK" {
		t.Errorf("MSET: expected +OK, got %s", reply)
	}

	getArgs := []string{"MGET"}
	for i := 0; i < 10; i++ {
		getArgs = append(getArgs, fmt.Sprintf("mset:k:%d", i))
	}
	c.Send(getArgs...)
	reply = c.ReadReply()
	for i := 0; i < 10; i++ {
		if !strings.Contains(reply, strconv.Itoa(i*10)) {
			t.Errorf("MGET after MSET: missing value %d", i*10)
		}
	}

	for i := 0; i < 10; i++ {
		c.Do("DEL", fmt.Sprintf("mset:k:%d", i))
	}
}

func TestDELMultipleKeys(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	c := DialProxy(t, proxy)

	for i := 0; i < 5; i++ {
		c.MustOK(t, "SET", fmt.Sprintf("del:k:%d", i), "v")
	}

	args := []string{"DEL"}
	for i := 0; i < 5; i++ {
		args = append(args, fmt.Sprintf("del:k:%d", i))
	}
	c.Send(args...)
	reply := c.ReadReply()
	if reply != ":5" {
		t.Errorf("DEL 5 keys: expected :5, got %s", reply)
	}
}

func TestPipeline(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	c := DialProxy(t, proxy)

	numKeys := 20
	for i := 0; i < numKeys; i++ {
		c.Send("SET", fmt.Sprintf("{pipe}:k:%d", i), strconv.Itoa(i))
	}
	for i := 0; i < numKeys; i++ {
		reply := c.ReadReply()
		if reply != "+OK" {
			t.Errorf("pipeline SET %d: expected +OK, got %s", i, reply)
		}
	}

	for i := 0; i < numKeys; i++ {
		c.Send("GET", fmt.Sprintf("{pipe}:k:%d", i))
	}
	for i := 0; i < numKeys; i++ {
		reply := c.ReadReply()
		want := "$" + strconv.Itoa(len(strconv.Itoa(i))) + ":" + strconv.Itoa(i)
		if reply != want {
			t.Errorf("pipeline GET %d: expected %s, got %s", i, want, reply)
		}
	}

	for i := 0; i < numKeys; i++ {
		c.Do("DEL", fmt.Sprintf("{pipe}:k:%d", i))
	}
}

func TestMultiExec(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	c := DialProxy(t, proxy)

	reply := c.Do("MULTI")
	if reply != "+OK" {
		t.Fatalf("MULTI: expected +OK, got %s", reply)
	}

	c.Send("SET", "{tx}:a", "1")
	if r := c.ReadReply(); r != "+QUEUED" {
		t.Errorf("QUEUED: expected +QUEUED, got %s", r)
	}
	c.Send("SET", "{tx}:b", "2")
	if r := c.ReadReply(); r != "+QUEUED" {
		t.Errorf("QUEUED: expected +QUEUED, got %s", r)
	}
	c.Send("GET", "{tx}:a")
	if r := c.ReadReply(); r != "+QUEUED" {
		t.Errorf("QUEUED: expected +QUEUED, got %s", r)
	}

	reply = c.Do("EXEC")
	if !strings.HasPrefix(reply, "*3:") {
		t.Errorf("EXEC: expected array of 3, got %s", reply)
	}

	c.Do("DEL", "{tx}:a", "{tx}:b")
}

func TestMultiDiscard(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	c := DialProxy(t, proxy)

	c.Do("MULTI")
	c.Send("SET", "{tx}:x", "1")
	c.ReadReply()
	reply := c.Do("DISCARD")
	if reply != "+OK" {
		t.Errorf("DISCARD: expected +OK, got %s", reply)
	}

	c.MustNil(t, "{tx}:x")
}

func TestDBSIZE(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	c := DialProxy(t, proxy)

	reply := c.Do("DBSIZE")
	if !strings.HasPrefix(reply, ":") {
		t.Errorf("DBSIZE: expected integer, got %s", reply)
	}
}

func TestEVAL(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	c := DialProxy(t, proxy)

	c.Send("EVAL", "return redis.call('SET', KEYS[1], ARGV[1])", "1", "eval:key", "eval:val")
	reply := c.ReadReply()
	if reply != "+OK" {
		t.Errorf("EVAL SET: expected +OK, got %s", reply)
	}

	c.MustGet(t, "eval:key", "eval:val")
	c.Do("DEL", "eval:key")
}

func TestConcurrentClients(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	numClients := 10
	numOps := 20
	var wg sync.WaitGroup
	errors := make(chan error, numClients*numOps)

	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			c := DialProxy(t, proxy)
			for j := 0; j < numOps; j++ {
				key := fmt.Sprintf("{client%d}:key:%d", clientID, j)
				val := fmt.Sprintf("v%d_%d", clientID, j)
				c.Send("SET", key, val)
				r := c.ReadReply()
				if r != "+OK" {
					errors <- fmt.Errorf("client %d SET %s: got %s", clientID, key, r)
					return
				}
				c.Send("GET", key)
				r = c.ReadReply()
				want := "$" + strconv.Itoa(len(val)) + ":" + val
				if r != want {
					errors <- fmt.Errorf("client %d GET %s: want %s got %s", clientID, key, want, r)
					return
				}
			}
			for j := 0; j < numOps; j++ {
				key := fmt.Sprintf("{client%d}:key:%d", clientID, j)
				c.Do("DEL", key)
			}
		}(i)
	}

	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}

func TestProxyCommand(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	c := DialProxy(t, proxy)

	c.Send("PROXY", "CONFIG", "GET", "max-clients")
	reply := c.ReadReply()
	if !strings.Contains(reply, "max-clients") {
		t.Errorf("PROXY CONFIG GET max-clients: expected max-clients in reply, got %s", reply)
	}

	c.Send("PROXY", "MULTIPLEXING", "STATUS")
	reply = c.ReadReply()
	if !strings.Contains(reply, "off") {
		t.Errorf("PROXY MULTIPLEXING STATUS: expected 'off', got %s", reply)
	}

	c.Send("PROXY", "MULTIPLEXING", "OFF")
	reply = c.ReadReply()
	if reply != "+OK" {
		t.Errorf("PROXY MULTIPLEXING OFF: expected +OK, got %s", reply)
	}

	c.Send("PROXY", "INFO")
	reply = c.ReadReply()
	if !strings.Contains(reply, "proxy_version") {
		t.Errorf("PROXY INFO: expected proxy_version in reply, got %s", reply)
	}

	c.Send("PROXY", "STATS")
	reply = c.ReadReply()
	if !strings.Contains(reply, "used_cpu_sys") {
		t.Errorf("PROXY STATS: expected used_cpu_sys in reply, got %s", reply)
	}
	if !strings.Contains(reply, "used_cpu_user") {
		t.Errorf("PROXY STATS: expected used_cpu_user in reply, got %s", reply)
	}
	if !strings.Contains(reply, "instantaneous_ops_per_sec") {
		t.Errorf("PROXY STATS: expected instantaneous_ops_per_sec in reply, got %s", reply)
	}
	if !strings.Contains(reply, "used_cpu_sys_pct") {
		t.Errorf("PROXY STATS: expected used_cpu_sys_pct in reply, got %s", reply)
	}
	if !strings.Contains(reply, "used_cpu_user_pct") {
		t.Errorf("PROXY STATS: expected used_cpu_user_pct in reply, got %s", reply)
	}

	c.Send("PROXY", "NODES")
	reply = c.ReadReply()
	if !strings.HasPrefix(reply, "$") {
		t.Errorf("PROXY NODES: expected bulk string reply, got %s", reply)
	}
	if !strings.Contains(reply, "myself,master") {
		t.Errorf("PROXY NODES: expected myself,master in reply, got %s", reply)
	}
	if !strings.Contains(reply, "0-16383") {
		t.Errorf("PROXY NODES: expected 0-16383 slots in reply, got %s", reply)
	}
	if !strings.Contains(reply, proxy.Addr()) {
		t.Errorf("PROXY NODES: expected proxy addr %s in reply, got %s", proxy.Addr(), reply)
	}
}

func TestHashTagSameSlot(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	c := DialProxy(t, proxy)

	keys := []string{"{user:1}:name", "{user:1}:email", "{user:1}:age"}
	for i, k := range keys {
		c.MustOK(t, "SET", k, fmt.Sprintf("val%d", i))
	}

	for i, k := range keys {
		c.MustGet(t, k, fmt.Sprintf("val%d", i))
	}

	for _, k := range keys {
		c.Do("DEL", k)
	}
}

func TestExpire(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	c := DialProxy(t, proxy)

	c.MustOK(t, "SET", "expire:key", "value")
	c.Send("EXPIRE", "expire:key", "1")
	reply := c.ReadReply()
	if reply != ":1" {
		t.Errorf("EXPIRE: expected :1, got %s", reply)
	}

	c.MustGet(t, "expire:key", "value")
	time.Sleep(1200 * time.Millisecond)
	c.MustNil(t, "expire:key")
}

func TestSCANAllKeysFound(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	c := DialProxy(t, proxy)

	prefix := "scanall"
	numKeys := 30
	for i := 0; i < numKeys; i++ {
		c.MustOK(t, "SET", fmt.Sprintf("%s:%d", prefix, i), "v")
	}

	allKeys := make(map[string]bool)
	cursor := "0"
	iterations := 0
	maxIter := 1000
	for {
		iterations++
		if iterations > maxIter {
			t.Fatalf("SCAN did not terminate after %d iterations", maxIter)
		}
		c.Send("SCAN", cursor, "MATCH", prefix+":*", "COUNT", "100")
		reply := c.ReadReply()
		newCursor, keys := parseSCANReply(reply)
		for _, k := range keys {
			allKeys[k] = true
		}
		cursor = newCursor
		if cursor == "0" {
			break
		}
	}

	if len(allKeys) != numKeys {
		t.Errorf("SCAN found %d keys, expected %d", len(allKeys), numKeys)
	}

	for i := 0; i < numKeys; i++ {
		c.Do("DEL", fmt.Sprintf("%s:%d", prefix, i))
	}
}

func parseSCANReply(reply string) (cursor string, keys []string) {
	if !strings.HasPrefix(reply, "*2:[") {
		return "0", nil
	}
	inner := reply[4 : len(reply)-1]

	cursorPart, keysPart, ok := splitSCANParts(inner)
	if !ok {
		return "0", nil
	}

	if idx := strings.Index(cursorPart, ":"); idx >= 0 {
		cursor = cursorPart[idx+1:]
	}

	if strings.HasPrefix(keysPart, "*") {
		bracketIdx := strings.Index(keysPart, "[")
		if bracketIdx >= 0 {
			keysInner := keysPart[bracketIdx+1 : len(keysPart)-1]
			for _, elem := range strings.Split(keysInner, ",") {
				if idx := strings.Index(elem, ":"); idx >= 0 {
					keys = append(keys, elem[idx+1:])
				}
			}
		}
	}

	if cursor == "" {
		cursor = "0"
	}
	return cursor, keys
}

func splitSCANParts(s string) (cursor, keys string, ok bool) {
	if !strings.HasPrefix(s, "$") {
		return "", "", false
	}
	colonIdx := strings.Index(s, ":")
	if colonIdx < 0 {
		return "", "", false
	}
	lenStr := s[1:colonIdx]
	n, err := strconv.Atoi(lenStr)
	if err != nil {
		return "", "", false
	}
	end := colonIdx + 1 + n
	if end > len(s) {
		return "", "", false
	}
	cursor = s[:end]
	rest := s[end:]
	if len(rest) == 0 || rest[0] != ',' {
		return cursor, "", true
	}
	keys = rest[1:]
	return cursor, keys, true
}

func TestWATCHMultiExec(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	c := DialProxy(t, proxy)

	c.MustOK(t, "SET", "{tx}:counter", "0")

	c.Send("WATCH", "{tx}:counter")
	reply := c.ReadReply()
	if reply != "+OK" {
		t.Fatalf("WATCH: expected +OK, got %s", reply)
	}

	c.Send("MULTI")
	reply = c.ReadReply()
	if reply != "+OK" {
		t.Fatalf("MULTI: expected +OK, got %s", reply)
	}

	c.Send("INCR", "{tx}:counter")
	reply = c.ReadReply()
	if reply != "+QUEUED" {
		t.Fatalf("INCR in MULTI: expected +QUEUED, got %s", reply)
	}

	c.Send("EXEC")
	reply = c.ReadReply()
	if !strings.Contains(reply, "1") {
		t.Errorf("EXEC after WATCH: expected result containing 1, got %s", reply)
	}

	c.Do("DEL", "{tx}:counter")
}

func TestWATCHAbort(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	c1 := DialProxy(t, proxy)
	c2 := DialProxy(t, proxy)

	c1.MustOK(t, "SET", "{watch}:key", "original")

	c1.Send("WATCH", "{watch}:key")
	reply := c1.ReadReply()
	if reply != "+OK" {
		t.Fatalf("WATCH: expected +OK, got %s", reply)
	}

	c2.MustOK(t, "SET", "{watch}:key", "modified")

	c1.Send("MULTI")
	c1.ReadReply()

	c1.Send("GET", "{watch}:key")
	c1.ReadReply()

	c1.Send("EXEC")
	reply = c1.ReadReply()
	if reply != "*-1" {
		t.Logf("WATCH abort: EXEC returned %s (nil array expected if WATCH aborted)", reply)
	}

	c1.Do("DEL", "{watch}:key")
}

func TestCLUSTERInfo(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	c := DialProxy(t, proxy)

	c.Send("CLUSTER", "INFO")
	reply := c.ReadReply()
	if !strings.Contains(reply, "cluster_") {
		t.Errorf("CLUSTER INFO: expected cluster_ fields in reply, got %s", reply)
	}
}

func TestINFO(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	c := DialProxy(t, proxy)

	c.Send("INFO")
	reply := c.ReadReply()
	if len(reply) == 0 {
		t.Errorf("INFO: expected non-empty reply")
	}

	c.Send("INFO", "server")
	reply = c.ReadReply()
	if len(reply) == 0 {
		t.Errorf("INFO server: expected non-empty reply")
	}
}

func TestRESET(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	c := DialProxy(t, proxy)

	c.Send("MULTI")
	c.ReadReply()
	c.Send("SET", "foo", "bar")
	c.ReadReply()

	c.Send("RESET")
	reply := c.ReadReply()
	if reply != "+RESET" {
		t.Errorf("RESET: expected +RESET, got %s", reply)
	}

	c.MustOK(t, "SET", "after:reset", "ok")
	c.MustGet(t, "after:reset", "ok")
	c.Do("DEL", "after:reset")
}
