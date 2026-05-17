package integration

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

)

func TestSORTCommand(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	key := "sort:list"
	c.Do("DEL", key)
	defer c.Do("DEL", key)

	c.Do("RPUSH", key, "3", "1", "2", "6", "4", "5")

	reply := c.Do("SORT", key)
	if !strings.HasPrefix(reply, "*6:") {
		t.Fatalf("SORT: expected array of 6, got %s", reply)
	}
	if !strings.Contains(reply, "1") || !strings.Contains(reply, "6") {
		t.Errorf("SORT: missing elements, got %s", reply)
	}

	reply = c.Do("SORT", key, "ASC")
	if !strings.HasPrefix(reply, "*6:") {
		t.Errorf("SORT ASC: expected array of 6, got %s", reply)
	}

	reply = c.Do("SORT", key, "DESC")
	if !strings.HasPrefix(reply, "*6:") {
		t.Errorf("SORT DESC: expected array of 6, got %s", reply)
	}

	reply = c.Do("SORT", key, "LIMIT", "1", "2")
	if !strings.HasPrefix(reply, "*2:") {
		t.Errorf("SORT LIMIT 1 2: expected array of 2, got %s", reply)
	}

	alphaKey := "sort:alpha"
	c.Do("DEL", alphaKey)
	defer c.Do("DEL", alphaKey)
	c.Do("RPUSH", alphaKey, "python", "c", "java", "javascript", "c++")

	reply = c.Do("SORT", alphaKey, "ALPHA")
	if !strings.HasPrefix(reply, "*5:") {
		t.Errorf("SORT ALPHA: expected array of 5, got %s", reply)
	}
	if !strings.Contains(reply, "c") {
		t.Errorf("SORT ALPHA: missing elements, got %s", reply)
	}

	reply = c.Do("SORT", alphaKey, "DESC", "ALPHA")
	if !strings.HasPrefix(reply, "*5:") {
		t.Errorf("SORT DESC ALPHA: expected array of 5, got %s", reply)
	}
}

func TestBITOPCommand(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.Do("DEL", "{k}1", "{k}2", "{k}3")
	defer c.Do("DEL", "{k}1", "{k}2", "{k}3")

	c.SendBinary([]byte("SET"), []byte("{k}1"), []byte("\x0f"))
	c.ReadReply()
	c.SendBinary([]byte("SET"), []byte("{k}2"), []byte("\xf1"))
	c.ReadReply()

	reply := c.Do("BITOP", "NOT", "{k}3", "{k}1")
	n := replyInt(t, reply)
	if n != 1 {
		t.Errorf("BITOP NOT: expected :1 (length), got %s", reply)
	}

	reply = c.Do("BITOP", "AND", "{k}3", "{k}1", "{k}2")
	n = replyInt(t, reply)
	if n != 1 {
		t.Errorf("BITOP AND: expected :1 (length), got %s", reply)
	}
}

func TestBITFIELDCommand(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.Do("DEL", "bitfield:key")
	defer c.Do("DEL", "bitfield:key")

	c.MustOK(t, "SET", "bitfield:key", "123")

	reply := c.Do("BITFIELD", "bitfield:key", "INCRBY", "i5", "2", "3")
	if replyIsError(reply) {
		t.Errorf("BITFIELD INCRBY: unexpected error %s", reply)
	}
	if !strings.HasPrefix(reply, "*1:") {
		t.Errorf("BITFIELD INCRBY: expected array of 1, got %s", reply)
	}

	c.Do("DEL", "bitfield:key")
	reply = c.Do("BITFIELD", "bitfield:key", "SET", "u8", "0", "200")
	if replyIsError(reply) {
		t.Errorf("BITFIELD SET: unexpected error %s", reply)
	}

	reply = c.Do("BITFIELD", "bitfield:key", "GET", "u8", "0")
	if replyIsError(reply) {
		t.Errorf("BITFIELD GET: unexpected error %s", reply)
	}
	if !strings.Contains(reply, "200") {
		t.Errorf("BITFIELD GET: expected 200, got %s", reply)
	}
}

func TestBitPosCommand(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.Do("DEL", "bitpos:key")
	defer c.Do("DEL", "bitpos:key")

	c.Do("SET", "bitpos:key", "\xff\xf0\x00")

	reply := c.Do("BITPOS", "bitpos:key", "0")
	n := replyInt(t, reply)
	if n != 12 {
		t.Errorf("BITPOS first 0 bit: expected 12, got %d", n)
	}

	reply = c.Do("BITPOS", "bitpos:key", "1")
	n = replyInt(t, reply)
	if n != 0 {
		t.Errorf("BITPOS first 1 bit: expected 0, got %d", n)
	}
}

func TestUNWATCHCommand(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.MustOK(t, "SET", "{unwatch}:key", "original")
	defer c.Do("DEL", "{unwatch}:key")

	reply := c.Do("WATCH", "{unwatch}:key")
	if reply != "+OK" {
		t.Fatalf("WATCH: expected +OK, got %s", reply)
	}

	reply = c.Do("UNWATCH")
	if reply != "+OK" {
		t.Fatalf("UNWATCH: expected +OK, got %s", reply)
	}

	c2 := DialProxy(t, proxy)
	c2.MustOK(t, "SET", "{unwatch}:key", "modified")

	c.Do("MULTI")
	c.Send("GET", "{unwatch}:key")
	c.ReadReply()

	reply = c.Do("EXEC")
	if reply == "*-1" {
		t.Error("EXEC after UNWATCH: transaction should NOT be aborted (UNWATCH cleared the watch)")
	}
	if !strings.HasPrefix(reply, "*1:") {
		t.Errorf("EXEC after UNWATCH: expected array of 1, got %s", reply)
	}
	if !strings.Contains(reply, "modified") {
		t.Errorf("EXEC after UNWATCH: expected modified value, got %s", reply)
	}
}

func TestLMOVECommand(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	src := "{lmove}:src"
	dst := "{lmove}:dst"
	c.Do("DEL", src, dst)
	defer c.Do("DEL", src, dst)

	c.Do("RPUSH", src, "a", "b", "c")

	reply := c.Do("LMOVE", src, dst, "LEFT", "RIGHT")
	if replyIsError(reply) {
		t.Skipf("LMOVE not supported by this Redis build: %s", reply)
	}
	if replyBulk(t, reply) != "a" {
		t.Errorf("LMOVE LEFT RIGHT: expected a, got %s", reply)
	}

	reply = c.Do("LRANGE", dst, "0", "-1")
	if !strings.Contains(reply, "a") {
		t.Errorf("LMOVE: dst should contain a, got %s", reply)
	}

	reply = c.Do("LLEN", src)
	if reply != ":2" {
		t.Errorf("LMOVE: src should have 2 elements, got %s", reply)
	}

	reply = c.Do("LMOVE", src, dst, "RIGHT", "LEFT")
	if replyBulk(t, reply) != "c" {
		t.Errorf("LMOVE RIGHT LEFT: expected c, got %s", reply)
	}
}

func TestGETDELCommand(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.MustOK(t, "SET", "getdel:key", "myval")
	defer c.Do("DEL", "getdel:key")

	reply := c.Do("GETDEL", "getdel:key")
	if replyIsError(reply) {
		t.Skipf("GETDEL not supported by this Redis build: %s", reply)
	}
	if replyBulk(t, reply) != "myval" {
		t.Errorf("GETDEL: expected myval, got %s", reply)
	}

	c.MustNil(t, "getdel:key")

	reply = c.Do("GETDEL", "getdel:nokey")
	if reply != "$-1" {
		t.Errorf("GETDEL missing: expected nil, got %s", reply)
	}
}

func TestGETEXCommand(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.MustOK(t, "SET", "getex:key", "myval")
	defer c.Do("DEL", "getex:key")

	reply := c.Do("GETEX", "getex:key", "EX", "100")
	if replyIsError(reply) {
		t.Skipf("GETEX not supported by this Redis build: %s", reply)
	}
	if replyBulk(t, reply) != "myval" {
		t.Errorf("GETEX EX: expected myval, got %s", reply)
	}

	reply = c.Do("TTL", "getex:key")
	n := replyInt(t, reply)
	if n <= 0 || n > 100 {
		t.Errorf("GETEX: TTL should be set, got %d", n)
	}

	reply = c.Do("GETEX", "getex:key", "PERSIST")
	if replyBulk(t, reply) != "myval" {
		t.Errorf("GETEX PERSIST: expected myval, got %s", reply)
	}

	reply = c.Do("TTL", "getex:key")
	if reply != ":-1" {
		t.Errorf("GETEX PERSIST: TTL should be removed, got %s", reply)
	}
}

func TestCopyCommand(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	src := "{copy}:src"
	dst := "{copy}:dst"
	c.Do("DEL", src, dst)
	defer c.Do("DEL", src, dst)

	c.MustOK(t, "SET", src, "srcval")

	reply := c.Do("COPY", src, dst)
	if reply != ":1" {
		t.Skipf("COPY not supported or cross-slot error: %s", reply)
	}

	c.MustGet(t, dst, "srcval")
	c.MustGet(t, src, "srcval")
}

func TestObjectEncodingVariants(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.MustOK(t, "SET", "enc:int", "12345")
	defer c.Do("DEL", "enc:int")

	reply := c.Do("OBJECT", "ENCODING", "enc:int")
	if replyIsError(reply) {
		t.Errorf("OBJECT ENCODING int: unexpected error %s", reply)
	}

	c.Do("DEL", "enc:list")
	c.Do("RPUSH", "enc:list", "a", "b", "c")
	defer c.Do("DEL", "enc:list")

	reply = c.Do("OBJECT", "ENCODING", "enc:list")
	if replyIsError(reply) {
		t.Errorf("OBJECT ENCODING list: unexpected error %s", reply)
	}
}

func TestSCANCountOption(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	prefix := "scancnt"
	numKeys := 20
	for i := 0; i < numKeys; i++ {
		c.MustOK(t, "SET", fmt.Sprintf("%s:%d", prefix, i), "v")
	}
	defer func() {
		for i := 0; i < numKeys; i++ {
			c.Do("DEL", fmt.Sprintf("%s:%d", prefix, i))
		}
	}()

	found := make(map[string]bool)
	cursor := "0"
	maxIter := 500
	for iter := 0; iter < maxIter; iter++ {
		c.Send("SCAN", cursor, "MATCH", prefix+":*", "COUNT", "5")
		reply := c.ReadReply()
		newCursor, keys := parseSCANReply(reply)
		for _, k := range keys {
			found[k] = true
		}
		cursor = newCursor
		if cursor == "0" {
			break
		}
		if iter == maxIter-1 {
			t.Fatalf("SCAN with COUNT did not terminate after %d iterations", maxIter)
		}
	}

	if len(found) != numKeys {
		t.Errorf("SCAN COUNT: found %d keys, expected %d", len(found), numKeys)
	}
}

func TestIncrDecrEdgeCases(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.Do("DEL", "incr:edge")
	defer c.Do("DEL", "incr:edge")

	reply := c.Do("INCR", "incr:edge")
	if reply != ":1" {
		t.Errorf("INCR on missing key: expected :1, got %s", reply)
	}

	c.MustOK(t, "SET", "incr:str", "notanumber")
	defer c.Do("DEL", "incr:str")

	reply = c.Do("INCR", "incr:str")
	if !replyIsError(reply) {
		t.Errorf("INCR on non-integer: expected error, got %s", reply)
	}

	c.Do("DEL", "incr:big")
	defer c.Do("DEL", "incr:big")
	c.MustOK(t, "SET", "incr:big", "9223372036854775806")
	reply = c.Do("INCR", "incr:big")
	if reply != ":9223372036854775807" {
		t.Errorf("INCR to max int64: expected :9223372036854775807, got %s", reply)
	}
}

func TestTypeCommand(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	types := []struct {
		key     string
		setup   func()
		expType string
	}{
		{"type:str", func() { c.MustOK(t, "SET", "type:str", "v") }, "+string"},
		{"type:list", func() { c.Do("RPUSH", "type:list", "a") }, "+list"},
		{"type:set", func() { c.Do("SADD", "type:set", "a") }, "+set"},
		{"type:zset", func() { c.Do("ZADD", "type:zset", "1", "a") }, "+zset"},
		{"type:hash", func() { c.Do("HSET", "type:hash", "f", "v") }, "+hash"},
	}

	for _, tc := range types {
		tc.setup()
		defer c.Do("DEL", tc.key)
		reply := c.Do("TYPE", tc.key)
		if reply != tc.expType {
			t.Errorf("TYPE %s: expected %s, got %s", tc.key, tc.expType, reply)
		}
	}

	reply := c.Do("TYPE", "type:nokey")
	if reply != "+none" {
		t.Errorf("TYPE missing key: expected +none, got %s", reply)
	}
}

func TestRenameNotSupported(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.MustOK(t, "SET", "rename:src", "val")
	defer c.Do("DEL", "rename:src", "rename:dst")

	reply := c.Do("RENAME", "rename:src", "rename:dst")
	if !replyIsError(reply) {
		t.Errorf("RENAME: expected error (not supported), got %s", reply)
	}
	if !strings.Contains(reply, "not supported") {
		t.Errorf("RENAME: expected 'not supported', got %s", reply)
	}
}

func TestMSETNX(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.Do("DEL", "{msnx}:a", "{msnx}:b")
	defer c.Do("DEL", "{msnx}:a", "{msnx}:b")

	reply := c.Do("MSETNX", "{msnx}:a", "1", "{msnx}:b", "2")
	if reply != ":1" {
		t.Errorf("MSETNX all new: expected :1, got %s", reply)
	}

	reply = c.Do("MSETNX", "{msnx}:a", "new", "{msnx}:c", "3")
	if reply != ":0" {
		t.Errorf("MSETNX some existing: expected :0, got %s", reply)
	}

	c.MustGet(t, "{msnx}:a", "1")
}

func TestClientIDAndGetName(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	reply := c.Do("CLIENT", "ID")
	if replyIsError(reply) {
		t.Errorf("CLIENT ID: unexpected error %s", reply)
	}
	if !strings.HasPrefix(reply, ":") {
		t.Errorf("CLIENT ID: expected integer, got %s", reply)
	}

	reply = c.Do("CLIENT", "SETNAME", "testconn")
	if reply != "+OK" {
		t.Errorf("CLIENT SETNAME: expected +OK, got %s", reply)
	}

	reply = c.Do("CLIENT", "GETNAME")
	if replyIsError(reply) {
		t.Errorf("CLIENT GETNAME: unexpected error %s", reply)
	}
	if reply == "$-1" {
		t.Errorf("CLIENT GETNAME: expected a name, got nil")
	}
}

func TestZRANGEWithREV(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	key := "{zrev}:z"
	c.Do("DEL", key)
	defer c.Do("DEL", key)

	c.Do("ZADD", key, "1", "a", "2", "b", "3", "c")

	reply := c.Do("ZRANGE", key, "0", "-1")
	if !strings.HasPrefix(reply, "*3:") {
		t.Errorf("ZRANGE asc: expected array of 3, got %s", reply)
	}
	if !strings.Contains(reply, "a") {
		t.Errorf("ZRANGE asc: expected a in result, got %s", reply)
	}

	reply = c.Do("ZREVRANGE", key, "0", "-1")
	if !strings.HasPrefix(reply, "*3:") {
		t.Errorf("ZREVRANGE: expected array of 3, got %s", reply)
	}
	if !strings.Contains(reply, "c") {
		t.Errorf("ZREVRANGE: expected c first, got %s", reply)
	}
}

func TestHSCANFullIteration(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	key := "{hscan}:h"
	c.Do("DEL", key)
	defer c.Do("DEL", key)

	numFields := 20
	for i := 0; i < numFields; i++ {
		c.Do("HSET", key, fmt.Sprintf("field%d", i), fmt.Sprintf("val%d", i))
	}

	found := make(map[string]bool)
	cursor := "0"
	maxIter := 100
	for iter := 0; iter < maxIter; iter++ {
		c.Send("HSCAN", key, cursor)
		reply := c.ReadReply()
		if !strings.HasPrefix(reply, "*2:") {
			t.Fatalf("HSCAN: expected array of 2, got %s", reply)
		}
		inner := reply[4 : len(reply)-1]
		cursorPart, fieldsPart, ok := splitSCANParts(inner)
		if !ok {
			break
		}
		if idx := strings.Index(cursorPart, ":"); idx >= 0 {
			cursor = cursorPart[idx+1:]
		}
		if strings.HasPrefix(fieldsPart, "*") {
			bracketIdx := strings.Index(fieldsPart, "[")
			if bracketIdx >= 0 {
				fieldsInner := fieldsPart[bracketIdx+1 : len(fieldsPart)-1]
				elems := strings.Split(fieldsInner, ",")
				for i := 0; i < len(elems)-1; i += 2 {
					if idx := strings.Index(elems[i], ":"); idx >= 0 {
						found[elems[i][idx+1:]] = true
					}
				}
			}
		}
		if cursor == "0" {
			break
		}
		if iter == maxIter-1 {
			t.Fatalf("HSCAN did not terminate after %d iterations", maxIter)
		}
	}

	if len(found) != numFields {
		t.Errorf("HSCAN: found %d fields, expected %d", len(found), numFields)
	}
}

func TestZSCANFullIteration(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	key := "{zscan}:z"
	c.Do("DEL", key)
	defer c.Do("DEL", key)

	numMembers := 20
	for i := 0; i < numMembers; i++ {
		c.Do("ZADD", key, strconv.Itoa(i), fmt.Sprintf("member%d", i))
	}

	found := make(map[string]bool)
	cursor := "0"
	maxIter := 100
	for iter := 0; iter < maxIter; iter++ {
		c.Send("ZSCAN", key, cursor)
		reply := c.ReadReply()
		if !strings.HasPrefix(reply, "*2:") {
			t.Fatalf("ZSCAN: expected array of 2, got %s", reply)
		}
		inner := reply[4 : len(reply)-1]
		cursorPart, membersPart, ok := splitSCANParts(inner)
		if !ok {
			break
		}
		if idx := strings.Index(cursorPart, ":"); idx >= 0 {
			cursor = cursorPart[idx+1:]
		}
		if strings.HasPrefix(membersPart, "*") {
			bracketIdx := strings.Index(membersPart, "[")
			if bracketIdx >= 0 {
				membersInner := membersPart[bracketIdx+1 : len(membersPart)-1]
				elems := strings.Split(membersInner, ",")
				for i := 0; i < len(elems)-1; i += 2 {
					if idx := strings.Index(elems[i], ":"); idx >= 0 {
						found[elems[i][idx+1:]] = true
					}
				}
			}
		}
		if cursor == "0" {
			break
		}
		if iter == maxIter-1 {
			t.Fatalf("ZSCAN did not terminate after %d iterations", maxIter)
		}
	}

	if len(found) != numMembers {
		t.Errorf("ZSCAN: found %d members, expected %d", len(found), numMembers)
	}
}

func TestMSETNXSameSlotAtomicity(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.Do("DEL", "{msnx2}:a", "{msnx2}:b")
	defer c.Do("DEL", "{msnx2}:a", "{msnx2}:b")

	reply := c.Do("MSETNX", "{msnx2}:a", "1", "{msnx2}:b", "2")
	if reply != ":1" {
		t.Errorf("MSETNX all new same-slot: expected :1, got %s", reply)
	}

	reply = c.Do("MSETNX", "{msnx2}:a", "new", "{msnx2}:c", "3")
	if reply != ":0" {
		t.Errorf("MSETNX partial existing same-slot: expected :0 (atomicity), got %s", reply)
	}
	c.MustGet(t, "{msnx2}:a", "1")
}

func TestUNWATCHAfterWATCHThenMULTI(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.MustOK(t, "SET", "{uwm2}:key", "original")
	defer c.Do("DEL", "{uwm2}:key")

	reply := c.Do("WATCH", "{uwm2}:key")
	if reply != "+OK" {
		t.Fatalf("WATCH: expected +OK, got %s", reply)
	}

	reply = c.Do("MULTI")
	if reply != "+OK" {
		t.Fatalf("MULTI after WATCH: expected +OK, got %s", reply)
	}

	reply = c.Do("UNWATCH")
	if !replyIsError(reply) {
		t.Errorf("UNWATCH in stateMulti: expected error (pluster limitation), got %s", reply)
	}

	reply = c.Do("GET", "{uwm2}:key")
	if reply != "+QUEUED" {
		t.Errorf("GET after UNWATCH error in WATCH+MULTI: expected +QUEUED, got %s", reply)
	}

	reply = c.Do("EXEC")
	if !strings.HasPrefix(reply, "*1:") {
		t.Errorf("EXEC after WATCH+MULTI+UNWATCH: expected array of 1, got %s", reply)
	}
}

func TestUNWATCHInsideMULTIReturnsError(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.MustOK(t, "SET", "{uwm}:key", "val")
	defer c.Do("DEL", "{uwm}:key")

	reply := c.Do("MULTI")
	if reply != "+OK" {
		t.Fatalf("MULTI: expected +OK, got %s", reply)
	}

	reply = c.Do("UNWATCH")
	if !replyIsError(reply) {
		t.Errorf("UNWATCH inside MULTI: expected error (pluster does not queue it), got %s", reply)
	}

	reply = c.Do("GET", "{uwm}:key")
	if reply != "+QUEUED" {
		t.Errorf("GET after UNWATCH error: expected +QUEUED (transaction still active), got %s", reply)
	}

	reply = c.Do("EXEC")
	if !strings.HasPrefix(reply, "*1:") {
		t.Errorf("EXEC after UNWATCH error: expected array of 1, got %s", reply)
	}
	if !strings.Contains(reply, "val") {
		t.Errorf("EXEC after UNWATCH error: expected val in result, got %s", reply)
	}
}

func TestDUMPNotSupported(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.MustOK(t, "SET", "dump:key", "val")
	defer c.Do("DEL", "dump:key")

	reply := c.Do("DUMP", "dump:key")
	if !replyIsError(reply) {
		t.Errorf("DUMP: expected 'not supported' error, got %s", reply)
	}
	if !strings.Contains(reply, "not supported") {
		t.Errorf("DUMP: expected 'not supported' in error, got %s", reply)
	}
}
