package integration

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/pluster/pluster/pkg/config"
)

func replyIsError(reply string) bool {
	return strings.HasPrefix(reply, "-")
}

func replyIsCrossSlot(reply string) bool {
	return strings.HasPrefix(reply, "-CROSSSLOT")
}

func replyIsNil(reply string) bool {
	return reply == "$-1"
}

func replyInt(t *testing.T, reply string) int64 {
	t.Helper()
	if !strings.HasPrefix(reply, ":") {
		t.Fatalf("expected integer reply, got %s", reply)
	}
	n, err := strconv.ParseInt(reply[1:], 10, 64)
	if err != nil {
		t.Fatalf("parse integer %s: %v", reply, err)
	}
	return n
}

func replyBulk(t *testing.T, reply string) string {
	t.Helper()
	if !strings.HasPrefix(reply, "$") {
		t.Fatalf("expected bulk reply, got %s", reply)
	}
	idx := strings.Index(reply, ":")
	if idx < 0 {
		t.Fatalf("malformed bulk reply: %s", reply)
	}
	return reply[idx+1:]
}

func TestStringCommands(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	t.Run("SET_GET", func(t *testing.T) {
		c.MustOK(t, "SET", "str:a", "hello")
		c.MustGet(t, "str:a", "hello")
		c.Do("DEL", "str:a")
	})

	t.Run("binary_safe", func(t *testing.T) {
		key := "str:bin"
		val := "foo\x00bar\r\nbaz"
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
		c.Do("DEL", key)
	})

	t.Run("SET_NX_XX", func(t *testing.T) {
		c.Do("DEL", "str:nx")
		reply := c.Do("SET", "str:nx", "v1", "NX")
		if reply != "+OK" {
			t.Errorf("SET NX on missing key: expected +OK, got %s", reply)
		}
		reply = c.Do("SET", "str:nx", "v2", "NX")
		if reply != "$-1" {
			t.Errorf("SET NX on existing key: expected nil, got %s", reply)
		}
		c.MustGet(t, "str:nx", "v1")

		reply = c.Do("SET", "str:nx", "v3", "XX")
		if reply != "+OK" {
			t.Errorf("SET XX on existing key: expected +OK, got %s", reply)
		}
		c.MustGet(t, "str:nx", "v3")
		c.Do("DEL", "str:nx")

		reply = c.Do("SET", "str:noexist", "v", "XX")
		if reply != "$-1" {
			t.Errorf("SET XX on missing key: expected nil, got %s", reply)
		}
	})

	t.Run("SETNX_SETEX_PSETEX", func(t *testing.T) {
		c.Do("DEL", "str:setnx")
		reply := c.Do("SETNX", "str:setnx", "val")
		if reply != ":1" {
			t.Errorf("SETNX missing: expected :1, got %s", reply)
		}
		reply = c.Do("SETNX", "str:setnx", "val2")
		if reply != ":0" {
			t.Errorf("SETNX existing: expected :0, got %s", reply)
		}
		c.Do("DEL", "str:setnx")

		c.MustOK(t, "SETEX", "str:setex", "10", "myval")
		c.MustGet(t, "str:setex", "myval")
		reply = c.Do("TTL", "str:setex")
		n := replyInt(t, reply)
		if n <= 0 || n > 10 {
			t.Errorf("SETEX TTL: expected 1-10, got %d", n)
		}
		c.Do("DEL", "str:setex")

		c.MustOK(t, "PSETEX", "str:psetex", "10000", "myval")
		c.MustGet(t, "str:psetex", "myval")
		reply = c.Do("PTTL", "str:psetex")
		pn := replyInt(t, reply)
		if pn <= 0 || pn > 10000 {
			t.Errorf("PSETEX PTTL: expected 1-10000, got %d", pn)
		}
		c.Do("DEL", "str:psetex")
	})

	t.Run("GETSET", func(t *testing.T) {
		c.Do("DEL", "str:gs")
		c.MustOK(t, "SET", "str:gs", "old")
		reply := c.Do("GETSET", "str:gs", "new")
		if replyBulk(t, reply) != "old" {
			t.Errorf("GETSET: expected old, got %s", reply)
		}
		c.MustGet(t, "str:gs", "new")
		c.Do("DEL", "str:gs")
	})

	t.Run("APPEND_STRLEN", func(t *testing.T) {
		c.Do("DEL", "str:app")
		reply := c.Do("APPEND", "str:app", "hello")
		if reply != ":5" {
			t.Errorf("APPEND: expected :5, got %s", reply)
		}
		reply = c.Do("APPEND", "str:app", " world")
		if reply != ":11" {
			t.Errorf("APPEND 2nd: expected :11, got %s", reply)
		}
		c.MustGet(t, "str:app", "hello world")
		reply = c.Do("STRLEN", "str:app")
		if reply != ":11" {
			t.Errorf("STRLEN: expected :11, got %s", reply)
		}
		c.Do("DEL", "str:app")
	})

	t.Run("INCR_DECR", func(t *testing.T) {
		c.Do("DEL", "str:ctr")
		reply := c.Do("INCR", "str:ctr")
		if reply != ":1" {
			t.Errorf("INCR: expected :1, got %s", reply)
		}
		reply = c.Do("INCRBY", "str:ctr", "5")
		if reply != ":6" {
			t.Errorf("INCRBY 5: expected :6, got %s", reply)
		}
		reply = c.Do("DECR", "str:ctr")
		if reply != ":5" {
			t.Errorf("DECR: expected :5, got %s", reply)
		}
		reply = c.Do("DECRBY", "str:ctr", "3")
		if reply != ":2" {
			t.Errorf("DECRBY 3: expected :2, got %s", reply)
		}
		c.Do("DEL", "str:ctr")
	})

	t.Run("INCRBYFLOAT", func(t *testing.T) {
		c.Do("DEL", "str:flt")
		c.MustOK(t, "SET", "str:flt", "10.5")
		reply := c.Do("INCRBYFLOAT", "str:flt", "0.1")
		if !strings.Contains(reply, "10.6") {
			t.Errorf("INCRBYFLOAT: expected ~10.6, got %s", reply)
		}
		c.Do("DEL", "str:flt")
	})

	t.Run("SETRANGE_GETRANGE", func(t *testing.T) {
		c.Do("DEL", "str:range")
		c.MustOK(t, "SET", "str:range", "Hello World")
		reply := c.Do("SETRANGE", "str:range", "6", "Redis")
		if reply != ":11" {
			t.Errorf("SETRANGE: expected :11, got %s", reply)
		}
		c.MustGet(t, "str:range", "Hello Redis")
		reply = c.Do("GETRANGE", "str:range", "0", "4")
		if replyBulk(t, reply) != "Hello" {
			t.Errorf("GETRANGE 0-4: expected Hello, got %s", reply)
		}
		c.Do("DEL", "str:range")
	})

	t.Run("SETBIT_GETBIT_BITCOUNT", func(t *testing.T) {
		c.Do("DEL", "str:bit")
		reply := c.Do("SETBIT", "str:bit", "7", "1")
		if reply != ":0" {
			t.Errorf("SETBIT: expected :0, got %s", reply)
		}
		reply = c.Do("GETBIT", "str:bit", "7")
		if reply != ":1" {
			t.Errorf("GETBIT 7: expected :1, got %s", reply)
		}
		reply = c.Do("GETBIT", "str:bit", "0")
		if reply != ":0" {
			t.Errorf("GETBIT 0: expected :0, got %s", reply)
		}
		reply = c.Do("BITCOUNT", "str:bit")
		if reply != ":1" {
			t.Errorf("BITCOUNT: expected :1, got %s", reply)
		}
		c.Do("DEL", "str:bit")
	})

	t.Run("TYPE_EXISTS", func(t *testing.T) {
		c.Do("DEL", "str:type")
		c.MustOK(t, "SET", "str:type", "val")
		reply := c.Do("TYPE", "str:type")
		if reply != "+string" {
			t.Errorf("TYPE: expected +string, got %s", reply)
		}
		reply = c.Do("EXISTS", "str:type")
		if reply != ":1" {
			t.Errorf("EXISTS: expected :1, got %s", reply)
		}
		c.Do("DEL", "str:type")
		reply = c.Do("EXISTS", "str:type")
		if reply != ":0" {
			t.Errorf("EXISTS after DEL: expected :0, got %s", reply)
		}
	})

	t.Run("concurrent_set_get", func(t *testing.T) {
		var wg sync.WaitGroup
		errs := make(chan string, 100)
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				cc := DialProxy(t, proxy)
				key := fmt.Sprintf("{str}:conc:%d", id)
				val := fmt.Sprintf("val%d", id)
				cc.MustOK(t, "SET", key, val)
				cc.MustGet(t, key, val)
				cc.Do("DEL", key)
			}(i)
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			t.Error(e)
		}
	})
}

func TestHashCommands(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	key := "{hash}:h1"
	c.Do("DEL", key)

	t.Run("HSET_HGET_HEXISTS_HDEL", func(t *testing.T) {
		reply := c.Do("HSET", key, "f1", "v1")
		if reply != ":1" {
			t.Errorf("HSET f1: expected :1, got %s", reply)
		}
		reply = c.Do("HSET", key, "f2", "v2")
		if reply != ":1" {
			t.Errorf("HSET f2: expected :1, got %s", reply)
		}
		reply = c.Do("HGET", key, "f1")
		if replyBulk(t, reply) != "v1" {
			t.Errorf("HGET f1: expected v1, got %s", reply)
		}
		reply = c.Do("HEXISTS", key, "f1")
		if reply != ":1" {
			t.Errorf("HEXISTS f1: expected :1, got %s", reply)
		}
		reply = c.Do("HEXISTS", key, "nofield")
		if reply != ":0" {
			t.Errorf("HEXISTS missing: expected :0, got %s", reply)
		}
		reply = c.Do("HDEL", key, "f2")
		if reply != ":1" {
			t.Errorf("HDEL: expected :1, got %s", reply)
		}
		reply = c.Do("HEXISTS", key, "f2")
		if reply != ":0" {
			t.Errorf("HEXISTS after HDEL: expected :0, got %s", reply)
		}
	})

	t.Run("HSETNX", func(t *testing.T) {
		reply := c.Do("HSETNX", key, "f1", "newval")
		if reply != ":0" {
			t.Errorf("HSETNX existing: expected :0, got %s", reply)
		}
		reply = c.Do("HSETNX", key, "fnew", "newval")
		if reply != ":1" {
			t.Errorf("HSETNX new: expected :1, got %s", reply)
		}
	})

	t.Run("HLEN_HKEYS_HVALS_HGETALL", func(t *testing.T) {
		c.Do("DEL", key)
		c.Do("HSET", key, "a", "1")
		c.Do("HSET", key, "b", "2")
		c.Do("HSET", key, "c", "3")
		reply := c.Do("HLEN", key)
		if reply != ":3" {
			t.Errorf("HLEN: expected :3, got %s", reply)
		}
		reply = c.Do("HKEYS", key)
		if !strings.HasPrefix(reply, "*3:") {
			t.Errorf("HKEYS: expected array of 3, got %s", reply)
		}
		reply = c.Do("HVALS", key)
		if !strings.HasPrefix(reply, "*3:") {
			t.Errorf("HVALS: expected array of 3, got %s", reply)
		}
		reply = c.Do("HGETALL", key)
		if !strings.HasPrefix(reply, "*6:") {
			t.Errorf("HGETALL: expected array of 6, got %s", reply)
		}
		if !strings.Contains(reply, "a") || !strings.Contains(reply, "1") {
			t.Errorf("HGETALL: missing fields, got %s", reply)
		}
	})

	t.Run("HMSET_HMGET", func(t *testing.T) {
		c.Do("DEL", key)
		c.MustOK(t, "HMSET", key, "x", "10", "y", "20", "z", "30")
		reply := c.Do("HMGET", key, "x", "y", "z", "missing")
		if !strings.HasPrefix(reply, "*4:") {
			t.Errorf("HMGET: expected array of 4, got %s", reply)
		}
		if !strings.Contains(reply, "10") || !strings.Contains(reply, "20") || !strings.Contains(reply, "30") {
			t.Errorf("HMGET: missing values, got %s", reply)
		}
		if !strings.Contains(reply, "$-1") {
			t.Errorf("HMGET: missing nil for missing field, got %s", reply)
		}
	})

	t.Run("HINCRBY_HINCRBYFLOAT", func(t *testing.T) {
		c.Do("DEL", key)
		c.Do("HSET", key, "num", "10")
		reply := c.Do("HINCRBY", key, "num", "5")
		if reply != ":15" {
			t.Errorf("HINCRBY: expected :15, got %s", reply)
		}
		reply = c.Do("HINCRBYFLOAT", key, "num", "0.5")
		if !strings.Contains(reply, "15.5") {
			t.Errorf("HINCRBYFLOAT: expected 15.5, got %s", reply)
		}
	})

	t.Run("HSCAN", func(t *testing.T) {
		c.Do("DEL", key)
		for i := 0; i < 5; i++ {
			c.Do("HSET", key, fmt.Sprintf("field%d", i), fmt.Sprintf("val%d", i))
		}
		reply := c.Do("HSCAN", key, "0")
		if !strings.HasPrefix(reply, "*2:") {
			t.Errorf("HSCAN: expected array of 2, got %s", reply)
		}
	})

	c.Do("DEL", key)
}

func TestListCommands(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	key := "{list}:l1"
	c.Do("DEL", key)

	t.Run("LPUSH_RPUSH_LLEN_LPOP_RPOP", func(t *testing.T) {
		reply := c.Do("RPUSH", key, "a", "b", "c")
		if reply != ":3" {
			t.Errorf("RPUSH: expected :3, got %s", reply)
		}
		reply = c.Do("LPUSH", key, "z")
		if reply != ":4" {
			t.Errorf("LPUSH: expected :4, got %s", reply)
		}
		reply = c.Do("LLEN", key)
		if reply != ":4" {
			t.Errorf("LLEN: expected :4, got %s", reply)
		}
		reply = c.Do("LPOP", key)
		if replyBulk(t, reply) != "z" {
			t.Errorf("LPOP: expected z, got %s", reply)
		}
		reply = c.Do("RPOP", key)
		if replyBulk(t, reply) != "c" {
			t.Errorf("RPOP: expected c, got %s", reply)
		}
	})

	t.Run("LINDEX_LRANGE_LTRIM", func(t *testing.T) {
		c.Do("DEL", key)
		c.Do("RPUSH", key, "a", "b", "c", "d", "e")
		reply := c.Do("LINDEX", key, "0")
		if replyBulk(t, reply) != "a" {
			t.Errorf("LINDEX 0: expected a, got %s", reply)
		}
		reply = c.Do("LINDEX", key, "-1")
		if replyBulk(t, reply) != "e" {
			t.Errorf("LINDEX -1: expected e, got %s", reply)
		}
		reply = c.Do("LRANGE", key, "0", "2")
		if !strings.HasPrefix(reply, "*3:") {
			t.Errorf("LRANGE 0-2: expected array of 3, got %s", reply)
		}
		c.MustOK(t, "LTRIM", key, "1", "3")
		reply = c.Do("LLEN", key)
		if reply != ":3" {
			t.Errorf("LTRIM LLEN: expected :3, got %s", reply)
		}
	})

	t.Run("LPUSHX_RPUSHX", func(t *testing.T) {
		c.Do("DEL", "{list}:lpx")
		reply := c.Do("LPUSHX", "{list}:lpx", "v")
		if reply != ":0" {
			t.Errorf("LPUSHX missing: expected :0, got %s", reply)
		}
		c.Do("RPUSH", "{list}:lpx", "existing")
		reply = c.Do("LPUSHX", "{list}:lpx", "front")
		if reply != ":2" {
			t.Errorf("LPUSHX existing: expected :2, got %s", reply)
		}
		c.Do("DEL", "{list}:lpx")
	})

	t.Run("LINSERT_LREM", func(t *testing.T) {
		c.Do("DEL", key)
		c.Do("RPUSH", key, "a", "b", "c")
		reply := c.Do("LINSERT", key, "BEFORE", "b", "x")
		if reply != ":4" {
			t.Errorf("LINSERT BEFORE: expected :4, got %s", reply)
		}
		reply = c.Do("LRANGE", key, "0", "-1")
		if !strings.Contains(reply, "x") {
			t.Errorf("LINSERT: x not found, got %s", reply)
		}
		c.Do("RPUSH", key, "a")
		reply = c.Do("LREM", key, "2", "a")
		if reply != ":2" {
			t.Errorf("LREM: expected :2, got %s", reply)
		}
	})

	t.Run("BLPOP_single_key", func(t *testing.T) {
		c.Do("DEL", "{list}:blp")
		c.Do("RPUSH", "{list}:blp", "val1")
		reply := c.Do("BLPOP", "{list}:blp", "1")
		if !strings.Contains(reply, "val1") {
			t.Errorf("BLPOP: expected val1, got %s", reply)
		}
	})

	t.Run("BLPOP_crossslot_error", func(t *testing.T) {
		reply := c.Do("BLPOP", "list:key1", "list:key2", "1")
		if !replyIsError(reply) {
			t.Errorf("BLPOP cross-slot: expected error, got %s", reply)
		}
	})

	c.Do("DEL", key)
}

func TestSetCommands(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	key := "{set}:s1"
	c.Do("DEL", key)

	t.Run("SADD_SCARD_SISMEMBER_SMEMBERS_SREM", func(t *testing.T) {
		reply := c.Do("SADD", key, "a", "b", "c", "d")
		if reply != ":4" {
			t.Errorf("SADD: expected :4, got %s", reply)
		}
		reply = c.Do("SCARD", key)
		if reply != ":4" {
			t.Errorf("SCARD: expected :4, got %s", reply)
		}
		reply = c.Do("SISMEMBER", key, "a")
		if reply != ":1" {
			t.Errorf("SISMEMBER a: expected :1, got %s", reply)
		}
		reply = c.Do("SISMEMBER", key, "z")
		if reply != ":0" {
			t.Errorf("SISMEMBER z: expected :0, got %s", reply)
		}
		reply = c.Do("SMEMBERS", key)
		if !strings.HasPrefix(reply, "*4:") {
			t.Errorf("SMEMBERS: expected array of 4, got %s", reply)
		}
		reply = c.Do("SREM", key, "a", "b")
		if reply != ":2" {
			t.Errorf("SREM: expected :2, got %s", reply)
		}
		reply = c.Do("SCARD", key)
		if reply != ":2" {
			t.Errorf("SCARD after SREM: expected :2, got %s", reply)
		}
	})

	t.Run("SRANDMEMBER_SPOP", func(t *testing.T) {
		c.Do("DEL", key)
		c.Do("SADD", key, "x", "y", "z")
		reply := c.Do("SRANDMEMBER", key)
		if replyIsError(reply) {
			t.Errorf("SRANDMEMBER: unexpected error %s", reply)
		}
		reply = c.Do("SRANDMEMBER", key, "2")
		if !strings.HasPrefix(reply, "*2:") {
			t.Errorf("SRANDMEMBER 2: expected array of 2, got %s", reply)
		}
		reply = c.Do("SPOP", key)
		if replyIsError(reply) {
			t.Errorf("SPOP: unexpected error %s", reply)
		}
		reply = c.Do("SCARD", key)
		if reply != ":2" {
			t.Errorf("SCARD after SPOP: expected :2, got %s", reply)
		}
	})

	t.Run("SINTERCARD", func(t *testing.T) {
		key2 := "{set}:s2"
		c.Do("DEL", key, key2)
		c.Do("SADD", key, "a", "b", "c")
		c.Do("SADD", key2, "b", "c", "d")
		reply := c.Do("SINTERCARD", "2", key, key2)
		if replyIsError(reply) {
			t.Skipf("SINTERCARD not supported (Redis < 7.0): %s", reply)
		}
		n := replyInt(t, reply)
		if n != 2 {
			t.Errorf("SINTERCARD: expected :2, got %d", n)
		}
		c.Do("DEL", key2)
	})

	t.Run("SSCAN", func(t *testing.T) {
		c.Do("DEL", key)
		for i := 0; i < 5; i++ {
			c.Do("SADD", key, fmt.Sprintf("m%d", i))
		}
		reply := c.Do("SSCAN", key, "0")
		if !strings.HasPrefix(reply, "*2:") {
			t.Errorf("SSCAN: expected array of 2, got %s", reply)
		}
	})

	c.Do("DEL", key)
}

func TestZSetCommands(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	key := "{zset}:z1"
	c.Do("DEL", key)

	t.Run("ZADD_ZCARD_ZSCORE_ZRANK_ZREVRANK", func(t *testing.T) {
		reply := c.Do("ZADD", key, "1", "a", "2", "b", "3", "c")
		if reply != ":3" {
			t.Errorf("ZADD: expected :3, got %s", reply)
		}
		reply = c.Do("ZCARD", key)
		if reply != ":3" {
			t.Errorf("ZCARD: expected :3, got %s", reply)
		}
		reply = c.Do("ZSCORE", key, "b")
		if !strings.Contains(reply, "2") {
			t.Errorf("ZSCORE b: expected 2, got %s", reply)
		}
		reply = c.Do("ZRANK", key, "a")
		if reply != ":0" {
			t.Errorf("ZRANK a: expected :0, got %s", reply)
		}
		reply = c.Do("ZREVRANK", key, "c")
		if reply != ":0" {
			t.Errorf("ZREVRANK c: expected :0, got %s", reply)
		}
	})

	t.Run("ZRANGE_ZREVRANGE", func(t *testing.T) {
		reply := c.Do("ZRANGE", key, "0", "-1")
		if !strings.HasPrefix(reply, "*3:") {
			t.Errorf("ZRANGE: expected array of 3, got %s", reply)
		}
		if !strings.Contains(reply, "a") {
			t.Errorf("ZRANGE: missing a, got %s", reply)
		}
		reply = c.Do("ZREVRANGE", key, "0", "-1")
		if !strings.HasPrefix(reply, "*3:") {
			t.Errorf("ZREVRANGE: expected array of 3, got %s", reply)
		}
	})

	t.Run("ZRANGEBYSCORE_ZREVRANGEBYSCORE_ZCOUNT", func(t *testing.T) {
		reply := c.Do("ZRANGEBYSCORE", key, "1", "2")
		if !strings.HasPrefix(reply, "*2:") {
			t.Errorf("ZRANGEBYSCORE 1-2: expected array of 2, got %s", reply)
		}
		reply = c.Do("ZREVRANGEBYSCORE", key, "3", "2")
		if !strings.HasPrefix(reply, "*2:") {
			t.Errorf("ZREVRANGEBYSCORE 3-2: expected array of 2, got %s", reply)
		}
		reply = c.Do("ZCOUNT", key, "1", "3")
		if reply != ":3" {
			t.Errorf("ZCOUNT 1-3: expected :3, got %s", reply)
		}
	})

	t.Run("ZPOPMIN_ZPOPMAX", func(t *testing.T) {
		c.Do("DEL", key)
		c.Do("ZADD", key, "1", "a", "2", "b", "3", "c")
		reply := c.Do("ZPOPMIN", key)
		if !strings.Contains(reply, "a") {
			t.Errorf("ZPOPMIN: expected a, got %s", reply)
		}
		reply = c.Do("ZPOPMAX", key)
		if !strings.Contains(reply, "c") {
			t.Errorf("ZPOPMAX: expected c, got %s", reply)
		}
	})

	t.Run("ZREM_ZREMRANGEBYSCORE_ZREMRANGEBYRANK", func(t *testing.T) {
		c.Do("DEL", key)
		c.Do("ZADD", key, "1", "a", "2", "b", "3", "c", "4", "d")
		reply := c.Do("ZREM", key, "a")
		if reply != ":1" {
			t.Errorf("ZREM: expected :1, got %s", reply)
		}
		reply = c.Do("ZREMRANGEBYSCORE", key, "3", "4")
		if reply != ":2" {
			t.Errorf("ZREMRANGEBYSCORE 3-4: expected :2, got %s", reply)
		}
		c.Do("DEL", key)
		c.Do("ZADD", key, "1", "a", "2", "b", "3", "c")
		reply = c.Do("ZREMRANGEBYRANK", key, "0", "0")
		if reply != ":1" {
			t.Errorf("ZREMRANGEBYRANK 0-0: expected :1, got %s", reply)
		}
	})

	t.Run("ZINCRBY", func(t *testing.T) {
		c.Do("DEL", key)
		c.Do("ZADD", key, "5", "m")
		reply := c.Do("ZINCRBY", key, "3", "m")
		if !strings.Contains(reply, "8") {
			t.Errorf("ZINCRBY: expected 8, got %s", reply)
		}
	})

	t.Run("ZSCAN", func(t *testing.T) {
		c.Do("DEL", key)
		c.Do("ZADD", key, "1", "a", "2", "b", "3", "c")
		reply := c.Do("ZSCAN", key, "0")
		if !strings.HasPrefix(reply, "*2:") {
			t.Errorf("ZSCAN: expected array of 2, got %s", reply)
		}
	})

	t.Run("ZRANGEBYLEX_ZREVRANGEBYLEX_ZLEXCOUNT_ZREMRANGEBYLEX", func(t *testing.T) {
		c.Do("DEL", key)
		c.Do("ZADD", key, "0", "a", "0", "b", "0", "c", "0", "d")
		reply := c.Do("ZRANGEBYLEX", key, "[a", "[c")
		if !strings.HasPrefix(reply, "*3:") {
			t.Errorf("ZRANGEBYLEX: expected array of 3, got %s", reply)
		}
		reply = c.Do("ZREVRANGEBYLEX", key, "[d", "[b")
		if !strings.HasPrefix(reply, "*3:") {
			t.Errorf("ZREVRANGEBYLEX: expected array of 3, got %s", reply)
		}
		reply = c.Do("ZLEXCOUNT", key, "[a", "[c")
		if reply != ":3" {
			t.Errorf("ZLEXCOUNT: expected :3, got %s", reply)
		}
		reply = c.Do("ZREMRANGEBYLEX", key, "[a", "[a")
		if reply != ":1" {
			t.Errorf("ZREMRANGEBYLEX: expected :1, got %s", reply)
		}
	})

	c.Do("DEL", key)
}

func TestGeoCommands(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	key := "{geo}:g1"
	c.Do("DEL", key)

	t.Run("GEOADD_GEOPOS_GEODIST_GEOHASH", func(t *testing.T) {
		reply := c.Do("GEOADD", key, "13.361389", "38.115556", "Palermo", "15.087269", "37.502669", "Catania")
		if reply != ":2" {
			t.Errorf("GEOADD: expected :2, got %s", reply)
		}
		reply = c.Do("GEOPOS", key, "Palermo")
		if !strings.HasPrefix(reply, "*1:") {
			t.Errorf("GEOPOS: expected array of 1, got %s", reply)
		}
		reply = c.Do("GEODIST", key, "Palermo", "Catania")
		if replyIsError(reply) {
			t.Errorf("GEODIST: unexpected error %s", reply)
		}
		if !strings.Contains(reply, "166") {
			t.Errorf("GEODIST: expected ~166km, got %s", reply)
		}
		reply = c.Do("GEOHASH", key, "Palermo")
		if !strings.HasPrefix(reply, "*1:") {
			t.Errorf("GEOHASH: expected array of 1, got %s", reply)
		}
	})

	c.Do("DEL", key)
}

func TestHyperLogLogCommands(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	key := "{hll}:h1"
	c.Do("DEL", key)

	t.Run("PFADD_PFCOUNT", func(t *testing.T) {
		reply := c.Do("PFADD", key, "a", "b", "c", "d", "e")
		if reply != ":1" {
			t.Errorf("PFADD: expected :1, got %s", reply)
		}
		reply = c.Do("PFCOUNT", key)
		n := replyInt(t, reply)
		if n < 4 || n > 6 {
			t.Errorf("PFCOUNT: expected ~5, got %d", n)
		}
		reply = c.Do("PFADD", key, "a", "b")
		if reply != ":0" {
			t.Errorf("PFADD duplicate: expected :0, got %s", reply)
		}
	})

	c.Do("DEL", key)
}

func TestLuaCommands(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	t.Run("EVAL_basic", func(t *testing.T) {
		c.Do("DEL", "{lua}:k1")
		reply := c.Do("EVAL", "return redis.call('SET', KEYS[1], ARGV[1])", "1", "{lua}:k1", "luaval")
		if reply != "+OK" {
			t.Errorf("EVAL SET: expected +OK, got %s", reply)
		}
		c.MustGet(t, "{lua}:k1", "luaval")
		c.Do("DEL", "{lua}:k1")
	})

	t.Run("EVAL_return_value", func(t *testing.T) {
		reply := c.Do("EVAL", "return 42", "0")
		if reply != ":42" {
			t.Errorf("EVAL return 42: expected :42, got %s", reply)
		}
	})

	t.Run("EVAL_crossslot_error", func(t *testing.T) {
		reply := c.Do("EVAL", "return redis.call('SET', KEYS[1], KEYS[2])", "2", "key:a", "key:b")
		if !replyIsError(reply) {
			t.Errorf("EVAL cross-slot: expected error, got %s", reply)
		}
	})

	t.Run("EVALSHA_after_SCRIPT_LOAD", func(t *testing.T) {
		c.Do("DEL", "{lua}:k2")
		reply := c.Do("SCRIPT", "LOAD", "return redis.call('SET', KEYS[1], ARGV[1])")
		if replyIsError(reply) {
			t.Fatalf("SCRIPT LOAD: unexpected error %s", reply)
		}
		sha := replyBulk(t, reply)
		reply = c.Do("EVALSHA", sha, "1", "{lua}:k2", "shaval")
		if reply != "+OK" {
			t.Errorf("EVALSHA: expected +OK, got %s", reply)
		}
		c.MustGet(t, "{lua}:k2", "shaval")
		c.Do("DEL", "{lua}:k2")
	})
}

func TestAuthCommands(t *testing.T) {
	t.Run("no_password_set", func(t *testing.T) {
		proxy := NewTestProxy(t, sharedCluster)
		c := DialProxy(t, proxy)
		reply := c.Do("AUTH", "anypassword")
		if !replyIsError(reply) {
			t.Errorf("AUTH no-password: expected error, got %s", reply)
		}
		if !strings.Contains(reply, "no password") {
			t.Errorf("AUTH no-password: expected 'no password' message, got %s", reply)
		}
		c.MustOK(t, "SET", "auth:k", "v")
		c.Do("DEL", "auth:k")
	})

	t.Run("correct_password", func(t *testing.T) {
		proxy := NewTestProxy(t, sharedCluster, config.WithClientPassword("secret123"))
		c := DialProxy(t, proxy)
		reply := c.Do("AUTH", "secret123")
		if reply != "+OK" {
			t.Errorf("AUTH correct: expected +OK, got %s", reply)
		}
		c.MustOK(t, "SET", "auth:k", "v")
		c.Do("DEL", "auth:k")
	})

	t.Run("wrong_password", func(t *testing.T) {
		proxy := NewTestProxy(t, sharedCluster, config.WithClientPassword("secret123"))
		c := DialProxy(t, proxy)
		reply := c.Do("AUTH", "wrongpass")
		if !replyIsError(reply) {
			t.Errorf("AUTH wrong: expected error, got %s", reply)
		}
		if !strings.Contains(reply, "WRONGPASS") {
			t.Errorf("AUTH wrong: expected WRONGPASS, got %s", reply)
		}
	})

	t.Run("username_password_form", func(t *testing.T) {
		proxy := NewTestProxy(t, sharedCluster, config.WithClientPassword("secret123"))
		c := DialProxy(t, proxy)
		reply := c.Do("AUTH", "default", "secret123")
		if reply != "+OK" {
			t.Errorf("AUTH user+pass: expected +OK, got %s", reply)
		}
	})

	t.Run("NOAUTH_blocks_commands", func(t *testing.T) {
		proxy := NewTestProxy(t, sharedCluster, config.WithClientPassword("secret123"))
		c := DialProxy(t, proxy)
		reply := c.Do("SET", "auth:k", "v")
		if !replyIsError(reply) {
			t.Errorf("NOAUTH: expected error, got %s", reply)
		}
		if !strings.Contains(reply, "NOAUTH") {
			t.Errorf("NOAUTH: expected NOAUTH message, got %s", reply)
		}
		reply = c.Do("GET", "auth:k")
		if !replyIsError(reply) {
			t.Errorf("NOAUTH GET: expected error, got %s", reply)
		}
		reply = c.Do("PING")
		if replyIsError(reply) {
			t.Errorf("PING without auth should be allowed, got %s", reply)
		}
	})
}

func TestClusterCommands(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	t.Run("CLUSTER_NODES", func(t *testing.T) {
		reply := c.Do("CLUSTER", "NODES")
		if replyIsError(reply) {
			t.Fatalf("CLUSTER NODES: unexpected error %s", reply)
		}
		if !strings.HasPrefix(reply, "$") {
			t.Fatalf("CLUSTER NODES: expected bulk string reply, got %s", reply)
		}
		colonIdx := strings.Index(reply, ":")
		if colonIdx < 0 {
			t.Fatalf("CLUSTER NODES: malformed bulk reply: %s", reply)
		}
		content := reply[colonIdx+1:]
		if !strings.Contains(content, "myself,master") {
			t.Errorf("CLUSTER NODES: expected 'myself,master' in reply, got %s", content)
		}
		if !strings.Contains(content, "0-16383") {
			t.Errorf("CLUSTER NODES: expected '0-16383' (all slots) in reply, got %s", content)
		}
		lines := strings.Split(strings.TrimSpace(content), "\n")
		if len(lines) != 1 {
			t.Errorf("CLUSTER NODES: expected exactly 1 node line (proxy itself), got %d lines: %s", len(lines), content)
		}
	})

	t.Run("CLUSTER_INFO", func(t *testing.T) {
		reply := c.Do("CLUSTER", "INFO")
		if !strings.Contains(reply, "cluster_") {
			t.Errorf("CLUSTER INFO: expected cluster_ fields, got %s", reply)
		}
	})

	t.Run("CLUSTER_KEYSLOT", func(t *testing.T) {
		reply := c.Do("CLUSTER", "KEYSLOT", "foo")
		if replyIsError(reply) {
			t.Errorf("CLUSTER KEYSLOT: unexpected error %s", reply)
		}
		n := replyInt(t, reply)
		if n < 0 || n > 16383 {
			t.Errorf("CLUSTER KEYSLOT: expected 0-16383, got %d", n)
		}
	})

	t.Run("CLUSTER_unknown_subcommand", func(t *testing.T) {
		reply := c.Do("CLUSTER", "FOOBAR")
		if !replyIsError(reply) {
			t.Errorf("CLUSTER FOOBAR: expected error, got %s", reply)
		}
		if !strings.Contains(reply, "unknown subcommand") {
			t.Errorf("CLUSTER FOOBAR: expected 'unknown subcommand', got %s", reply)
		}
	})

	t.Run("CLUSTER_CONNECTION", func(t *testing.T) {
		freshProxy := NewTestProxy(t, sharedCluster, config.WithPoolSize(20))
		fc := DialProxy(t, freshProxy)

		fc.MustOK(t, "SET", "conn:warmup", "1")
		fc.Do("GET", "conn:warmup")
		fc.Do("DEL", "conn:warmup")

		reply := fc.Do("CLUSTER", "CONNECTION")
		if replyIsError(reply) {
			t.Fatalf("CLUSTER CONNECTION: unexpected error %s", reply)
		}
		if !strings.HasPrefix(reply, "$") {
			t.Errorf("CLUSTER CONNECTION: expected bulk string reply, got %s", reply)
		}
		colonIdx := strings.Index(reply, ":")
		if colonIdx < 0 {
			t.Fatalf("CLUSTER CONNECTION: malformed bulk reply: %s", reply)
		}
		content := reply[colonIdx+1:]
		lines := strings.Split(strings.TrimSpace(content), "\n")
		masterCount := 0
		for _, line := range lines {
			if line == "" {
				continue
			}
			trimmed := strings.TrimLeft(line, " ")
			isReplica := strings.HasPrefix(line, " ")
			if !isReplica {
				masterCount++
				if !strings.HasPrefix(trimmed, "master_") {
					t.Errorf("CLUSTER CONNECTION: master line should start with 'master_': %q", line)
					continue
				}
			} else {
				if !strings.HasPrefix(trimmed, "slave_node_") {
					t.Errorf("CLUSTER CONNECTION: replica line should start with 'slave_node_': %q", line)
					continue
				}
			}
			if !strings.Contains(line, "connection:") {
				t.Errorf("CLUSTER CONNECTION: line missing 'connection:': %q", line)
			}
			parts := strings.Fields(trimmed)
			if len(parts) < 3 {
				t.Errorf("CLUSTER CONNECTION: line malformed (expected 3+ parts): %q", line)
				continue
			}
			var addrPort string
			if strings.HasPrefix(parts[0], "slave_node_") {
				addrPort = strings.TrimPrefix(parts[0], "slave_node_")
			} else {
				addrPart := strings.SplitN(parts[0], "_", 2)
				if len(addrPart) >= 2 {
					addrPort = addrPart[1]
				}
			}
			if !strings.Contains(addrPort, ":") {
				t.Errorf("CLUSTER CONNECTION: expected addr:port in %q", parts[0])
			}
		}
		if masterCount == 0 {
			t.Errorf("CLUSTER CONNECTION: expected at least one master, got 0 in: %s", reply)
		}
	})
}

func TestUnsupportedCommands(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	notAllowed := []string{
		"SMOVE", "SUNION", "SINTER", "SDIFF",
		"SUNIONSTORE", "SINTERSTORE", "SDIFFSTORE",
		"RPOPLPUSH", "RENAME", "RENAMENX", "MOVE",
		"DUMP", "RESTORE",
		"ZUNIONSTORE", "ZINTERSTORE", "ZDIFFSTORE",
		"ZUNION", "ZINTER", "ZDIFF",
		"PFMERGE", "RANDOMKEY",
	}

	for _, cmd := range notAllowed {
		t.Run(cmd, func(t *testing.T) {
			reply := c.Do(cmd, "k1", "k2")
			if !replyIsError(reply) {
				t.Errorf("%s: expected error (not supported), got %s", cmd, reply)
			}
			if !strings.Contains(reply, "not supported") {
				t.Errorf("%s: expected 'not supported' message, got %s", cmd, reply)
			}
		})
	}
}

func TestUnknownCommands(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	unknowns := []string{"FOOBAR", "XYZZY", "NOTACOMMAND"}
	for _, cmd := range unknowns {
		t.Run(cmd, func(t *testing.T) {
			reply := c.Do(cmd, "arg1")
			if !replyIsError(reply) {
				t.Errorf("%s: expected error, got %s", cmd, reply)
			}
			if !strings.Contains(reply, "unknown command") {
				t.Errorf("%s: expected 'unknown command', got %s", cmd, reply)
			}
		})
	}
}

func TestSelectCommand(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	t.Run("SELECT_0_ok", func(t *testing.T) {
		reply := c.Do("SELECT", "0")
		if reply != "+OK" {
			t.Errorf("SELECT 0: expected +OK, got %s", reply)
		}
	})

	t.Run("SELECT_1_error", func(t *testing.T) {
		reply := c.Do("SELECT", "1")
		if !replyIsError(reply) {
			t.Errorf("SELECT 1: expected error in cluster mode, got %s", reply)
		}
	})
}

func TestMultislotCrossSlotEnabled(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	keys := make([]string, 8)
	vals := make([]string, 8)
	for i := 0; i < 8; i++ {
		keys[i] = fmt.Sprintf("multi:k:%d", i)
		vals[i] = fmt.Sprintf("val%d", i)
	}

	t.Run("MSET_cross_slot", func(t *testing.T) {
		args := []string{"MSET"}
		for i := 0; i < 8; i++ {
			args = append(args, keys[i], vals[i])
		}
		c.Send(args...)
		reply := c.ReadReply()
		if reply != "+OK" {
			t.Errorf("MSET cross-slot: expected +OK, got %s", reply)
		}
	})

	t.Run("MGET_cross_slot_values_correct", func(t *testing.T) {
		args := append([]string{"MGET"}, keys...)
		c.Send(args...)
		reply := c.ReadReply()
		if !strings.HasPrefix(reply, "*8:") {
			t.Errorf("MGET cross-slot: expected array of 8, got %s", reply)
		}
		for i := 0; i < 8; i++ {
			if !strings.Contains(reply, vals[i]) {
				t.Errorf("MGET cross-slot: missing val%d in reply", i)
			}
		}
	})

	for _, k := range keys {
		c.Do("DEL", k)
	}
}

func TestMultiExecCommands(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	t.Run("MULTI_SET_GET_EXEC_value_verify", func(t *testing.T) {
		c.Do("DEL", "{tx2}:k")
		c.MustOK(t, "MULTI")
		c.Send("SET", "{tx2}:k", "txval")
		if r := c.ReadReply(); r != "+QUEUED" {
			t.Fatalf("QUEUED: expected +QUEUED, got %s", r)
		}
		c.Send("GET", "{tx2}:k")
		if r := c.ReadReply(); r != "+QUEUED" {
			t.Fatalf("QUEUED: expected +QUEUED, got %s", r)
		}
		reply := c.Do("EXEC")
		if !strings.HasPrefix(reply, "*2:") {
			t.Fatalf("EXEC: expected array of 2, got %s", reply)
		}
		if !strings.Contains(reply, "OK") {
			t.Errorf("EXEC: expected OK in results, got %s", reply)
		}
		if !strings.Contains(reply, "txval") {
			t.Errorf("EXEC: expected txval in results, got %s", reply)
		}
		c.Do("DEL", "{tx2}:k")
	})

	t.Run("DISCARD", func(t *testing.T) {
		c.MustOK(t, "MULTI")
		c.Send("SET", "{tx2}:discard", "v")
		c.ReadReply()
		reply := c.Do("DISCARD")
		if reply != "+OK" {
			t.Errorf("DISCARD: expected +OK, got %s", reply)
		}
		c.MustNil(t, "{tx2}:discard")
	})

	t.Run("EXEC_without_MULTI", func(t *testing.T) {
		reply := c.Do("EXEC")
		if !replyIsError(reply) {
			t.Errorf("EXEC without MULTI: expected error, got %s", reply)
		}
	})

	t.Run("DISCARD_without_MULTI", func(t *testing.T) {
		reply := c.Do("DISCARD")
		if !replyIsError(reply) {
			t.Errorf("DISCARD without MULTI: expected error, got %s", reply)
		}
	})
}

func TestTimeCommand(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	t.Run("TIME_returns_array", func(t *testing.T) {
		spawnClients(t, proxy, 5, func(c *RedisConn, idx int) {
			reply := c.Do("TIME")
			if !strings.HasPrefix(reply, "*2:") {
				t.Errorf("client %d TIME: expected *2: array, got %s", idx, reply)
				return
			}
			parts := strings.SplitN(reply[3:], ":", 3)
			if len(parts) < 2 {
				t.Errorf("client %d TIME: malformed reply %s", idx, reply)
			}
		})
	})
}

func TestObjectCommand(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	t.Run("OBJECT_subcommands", func(t *testing.T) {
		spawnClients(t, proxy, 5, func(c *RedisConn, idx int) {
			key := fmt.Sprintf("obj:cmd:test:%d", idx)
			c.MustOK(t, "SET", key, "v")

			reply := c.Do("OBJECT", "REFCOUNT", key)
			if replyIsError(reply) {
				t.Errorf("client %d OBJECT REFCOUNT: unexpected error %s", idx, reply)
			}

			reply = c.Do("OBJECT", "ENCODING", key)
			if replyIsError(reply) {
				t.Errorf("client %d OBJECT ENCODING: unexpected error %s", idx, reply)
			}

			reply = c.Do("OBJECT", "IDLETIME", key)
			if replyIsError(reply) {
				t.Errorf("client %d OBJECT IDLETIME: unexpected error %s", idx, reply)
			}

			c.Do("DEL", key)
		})
	})
}

func TestReadonlyCommand(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	t.Run("READONLY_ok", func(t *testing.T) {
		spawnClients(t, proxy, 5, func(c *RedisConn, idx int) {
			reply := c.Do("READONLY")
			if reply != "+OK" {
				t.Errorf("client %d READONLY: expected +OK, got %s", idx, reply)
			}
		})
	})

	t.Run("READONLY_wrong_arity", func(t *testing.T) {
		c := DialProxy(t, proxy)
		reply := c.Do("READONLY", "xxxx")
		if !replyIsError(reply) {
			t.Errorf("READONLY with extra arg: expected error, got %s", reply)
		}
	})
}

func TestPipelineCommands(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	t.Run("pipelined_SET_GET_value_verify", func(t *testing.T) {
		n := 15
		for i := 0; i < n; i++ {
			c.Send("SET", fmt.Sprintf("{pipe2}:k:%d", i), fmt.Sprintf("pipeval%d", i))
		}
		for i := 0; i < n; i++ {
			reply := c.ReadReply()
			if reply != "+OK" {
				t.Errorf("pipeline SET %d: expected +OK, got %s", i, reply)
			}
		}
		for i := 0; i < n; i++ {
			c.Send("GET", fmt.Sprintf("{pipe2}:k:%d", i))
		}
		for i := 0; i < n; i++ {
			reply := c.ReadReply()
			want := fmt.Sprintf("pipeval%d", i)
			if replyBulk(t, reply) != want {
				t.Errorf("pipeline GET %d: expected %s, got %s", i, want, reply)
			}
		}
		for i := 0; i < n; i++ {
			c.Do("DEL", fmt.Sprintf("{pipe2}:k:%d", i))
		}
	})

	t.Run("pipelined_MGET", func(t *testing.T) {
		proxy2 := NewTestProxy(t, sharedCluster)
		c2 := DialProxy(t, proxy2)
		keys := []string{"{pg}:a", "{pg}:b", "{pg}:c"}
		for i, k := range keys {
			c2.MustOK(t, "SET", k, fmt.Sprintf("v%d", i))
		}
		c2.Send("MGET", keys[0], keys[1], keys[2])
		reply := c2.ReadReply()
		if !strings.HasPrefix(reply, "*3:") {
			t.Errorf("pipelined MGET: expected array of 3, got %s", reply)
		}
		for i := 0; i < 3; i++ {
			if !strings.Contains(reply, fmt.Sprintf("v%d", i)) {
				t.Errorf("pipelined MGET: missing v%d, got %s", i, reply)
			}
		}
		for _, k := range keys {
			c2.Do("DEL", k)
		}
	})
}

func TestSCANInvalidCursor(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	t.Run("non_numeric_cursor", func(t *testing.T) {
		reply := c.Do("SCAN", "hello")
		if !replyIsError(reply) {
			t.Errorf("SCAN with non-numeric cursor: expected error, got %s", reply)
		}
	})

	t.Run("overflow_cursor", func(t *testing.T) {
		reply := c.Do("SCAN", "18446744073709551716")
		if !replyIsError(reply) {
			t.Errorf("SCAN with overflow cursor: expected error, got %s", reply)
		}
	})
}

func TestEVALMultiKeySameSlot(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	tag := "{evalmulti}"
	numKeys := 3
	keys := make([]string, numKeys)
	for i := 0; i < numKeys; i++ {
		keys[i] = fmt.Sprintf("K:%s:%d:\x00\r\n:K", tag, i)
	}

	script := "local r={} for i=1,#KEYS do r[i]=KEYS[i] end return r"
	args := []string{"EVAL", script, strconv.Itoa(numKeys)}
	for _, k := range keys {
		args = append(args, k)
	}
	c.Send(args...)
	reply := c.ReadReply()
	if replyIsError(reply) {
		t.Fatalf("EVAL multi-key same slot: unexpected error %s", reply)
	}
	if !strings.HasPrefix(reply, fmt.Sprintf("*%d:", numKeys)) {
		t.Errorf("EVAL multi-key: expected array of %d, got %s", numKeys, reply)
	}
	for _, k := range keys {
		if !strings.Contains(reply, k) {
			t.Errorf("EVAL multi-key: missing key %q in reply %s", k, reply)
		}
	}
}

func TestClientSetNameTooLong(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	spawnClients(t, proxy, 5, func(c *RedisConn, idx int) {
		longName := strings.Repeat("a", 1000)
		reply := c.Do("CLIENT", "SETNAME", longName)
		if !replyIsError(reply) {
			t.Errorf("client %d CLIENT SETNAME too long: expected error, got %s", idx, reply)
		}
	})
}

func TestLargeScaleSCANAllKeys(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	numKeys := 500
	prefix := "lsscan"
	c := DialProxy(t, proxy)
	for i := 0; i < numKeys; i++ {
		c.MustOK(t, "SET", fmt.Sprintf("%s:%d", prefix, i), strconv.Itoa(i))
	}

	spawnClients(t, proxy, lsNumClients, func(cl *RedisConn, idx int) {
		found := make(map[string]bool)
		cursor := "0"
		maxIter := 5000
		for iter := 0; iter < maxIter; iter++ {
			cl.Send("SCAN", cursor, "MATCH", prefix+":*", "COUNT", "100")
			reply := cl.ReadReply()
			if replyIsError(reply) {
				t.Errorf("client %d SCAN: unexpected error %s", idx, reply)
				return
			}
			newCursor, keys := parseSCANReply(reply)
			for _, k := range keys {
				found[k] = true
			}
			cursor = newCursor
			if cursor == "0" {
				break
			}
			if iter == maxIter-1 {
				t.Errorf("client %d SCAN: did not terminate after %d iterations", idx, maxIter)
				return
			}
		}
		if len(found) != numKeys {
			t.Errorf("client %d SCAN: found %d keys, expected %d", idx, len(found), numKeys)
		}
	})

	for i := 0; i < numKeys; i++ {
		c.Do("DEL", fmt.Sprintf("%s:%d", prefix, i))
	}
}
