package integration

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

)

const (
	lsNumKeys    = 500
	lsNumLists   = 50
	lsNumClients = 5
)

func binaryKey(n int) string {
	return fmt.Sprintf("k:%d:\x00\r\n:k", n)
}

func binaryVal(n, dataLen int) string {
	base := strconv.Itoa(n)
	s := strings.Repeat(base, dataLen/len(base)+1)[:dataLen]
	return s + "\x00\r\n" + strconv.Itoa(n)
}

func spawnClients(t *testing.T, proxy *TestProxy, numClients int, fn func(c *RedisConn, clientIdx int)) {
	t.Helper()
	var wg sync.WaitGroup
	errs := make(chan error, numClients*10)
	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			c := DialProxy(t, proxy)
			fn(c, idx)
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestLargeScaleStringCommands(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large-scale test in short mode")
	}
	proxy := NewTestProxy(t, sharedCluster)

	for _, dataLen := range []int{1, 64} {
		dataLen := dataLen
		t.Run(fmt.Sprintf("SET_%d_keys_size_%d_clients_%d", lsNumKeys, dataLen+4, lsNumClients), func(t *testing.T) {
			spawnClients(t, proxy, lsNumClients, func(c *RedisConn, idx int) {
				for n := 0; n < lsNumKeys; n++ {
					key := binaryKey(n)
					val := binaryVal(n, dataLen)
					c.SendBinary([]byte("SET"), []byte(key), []byte(val))
					reply := c.ReadReply()
					if reply != "+OK" {
						t.Errorf("client %d SET key %d: expected +OK, got %s", idx, n, reply)
						return
					}
				}
			})
		})

		t.Run(fmt.Sprintf("GET_%d_keys_size_%d_clients_%d", lsNumKeys, dataLen+4, lsNumClients), func(t *testing.T) {
			spawnClients(t, proxy, lsNumClients, func(c *RedisConn, idx int) {
				for n := 0; n < lsNumKeys; n++ {
					key := binaryKey(n)
					val := binaryVal(n, dataLen)
					c.SendBinary([]byte("GET"), []byte(key))
					reply := c.ReadReply()
					want := "$" + strconv.Itoa(len(val)) + ":" + val
					if reply != want {
						t.Errorf("client %d GET key %d: expected %q, got %q", idx, n, want, reply)
						return
					}
				}
			})
		})
	}

	spawnClients(t, proxy, 1, func(c *RedisConn, idx int) {
		for n := 0; n < lsNumKeys; n++ {
			c.Do("DEL", binaryKey(n))
		}
	})
}

func TestLargeScaleHashCommands(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large-scale test in short mode")
	}
	proxy := NewTestProxy(t, sharedCluster)

	t.Run(fmt.Sprintf("HSET_%d_keys_clients_%d", lsNumKeys, lsNumClients), func(t *testing.T) {
		spawnClients(t, proxy, lsNumClients, func(c *RedisConn, idx int) {
			for n := 0; n < lsNumKeys; n++ {
				key := fmt.Sprintf("myhashs:%d:\x00\r\n:myhashs", n)
				for _, f := range []string{"a", "b", "c"} {
					field := fmt.Sprintf("%s:\x00\r\n:%s", f, f)
					val := field
					c.SendBinary([]byte("HSET"), []byte(key), []byte(field), []byte(val))
					reply := c.ReadReply()
					if replyIsError(reply) {
						t.Errorf("client %d HSET key %d field %s: %s", idx, n, f, reply)
						return
					}
				}
			}
		})
	})

	t.Run(fmt.Sprintf("HGET_%d_keys_clients_%d", lsNumKeys, lsNumClients), func(t *testing.T) {
		spawnClients(t, proxy, lsNumClients, func(c *RedisConn, idx int) {
			for n := 0; n < lsNumKeys; n++ {
				key := fmt.Sprintf("myhashs:%d:\x00\r\n:myhashs", n)
				for _, f := range []string{"a", "b", "c"} {
					field := fmt.Sprintf("%s:\x00\r\n:%s", f, f)
					val := field
					c.SendBinary([]byte("HGET"), []byte(key), []byte(field))
					reply := c.ReadReply()
					want := "$" + strconv.Itoa(len(val)) + ":" + val
					if reply != want {
						t.Errorf("client %d HGET key %d field %s: expected %q, got %q", idx, n, f, want, reply)
						return
					}
				}
			}
		})
	})

	t.Run(fmt.Sprintf("HEXISTS_%d_keys_clients_%d", lsNumKeys, lsNumClients), func(t *testing.T) {
		spawnClients(t, proxy, lsNumClients, func(c *RedisConn, idx int) {
			for n := 0; n < lsNumKeys; n++ {
				key := fmt.Sprintf("myhashs:%d:\x00\r\n:myhashs", n)
				for _, f := range []string{"a", "b", "c"} {
					field := fmt.Sprintf("%s:\x00\r\n:%s", f, f)
					c.SendBinary([]byte("HEXISTS"), []byte(key), []byte(field))
					reply := c.ReadReply()
					if reply != ":1" {
						t.Errorf("client %d HEXISTS key %d field %s: expected :1, got %s", idx, n, f, reply)
						return
					}
				}
			}
		})
	})

	t.Run(fmt.Sprintf("HSTRLEN_%d_keys_clients_%d", lsNumKeys, lsNumClients), func(t *testing.T) {
		spawnClients(t, proxy, lsNumClients, func(c *RedisConn, idx int) {
			for n := 0; n < lsNumKeys; n++ {
				key := fmt.Sprintf("myhashs:%d:\x00\r\n:myhashs", n)
				for _, f := range []string{"a", "b", "c"} {
					field := fmt.Sprintf("%s:\x00\r\n:%s", f, f)
					val := field
					c.SendBinary([]byte("HSTRLEN"), []byte(key), []byte(field))
					reply := c.ReadReply()
					expected := ":" + strconv.Itoa(len(val))
					if reply != expected {
						t.Errorf("client %d HSTRLEN key %d field %s: expected %s, got %s", idx, n, f, expected, reply)
						return
					}
				}
			}
		})
	})

	t.Run(fmt.Sprintf("HSETNX_%d_keys_clients_%d", lsNumKeys, lsNumClients), func(t *testing.T) {
		spawnClients(t, proxy, lsNumClients, func(c *RedisConn, idx int) {
			for n := 0; n < lsNumKeys; n++ {
				key := fmt.Sprintf("myhashs:%d:\x00\r\n:myhashs", n)
				for _, f := range []string{"a", "b", "c"} {
					field := fmt.Sprintf("%s:\x00\r\n:%s", f, f)
					c.SendBinary([]byte("HSETNX"), []byte(key), []byte(field), []byte("newval"))
					reply := c.ReadReply()
					if reply != ":0" {
						t.Errorf("client %d HSETNX existing key %d field %s: expected :0, got %s", idx, n, f, reply)
						return
					}
				}
			}
		})
	})

	spawnClients(t, proxy, 1, func(c *RedisConn, idx int) {
		for n := 0; n < lsNumKeys; n++ {
			key := fmt.Sprintf("myhashs:%d:\x00\r\n:myhashs", n)
			c.Do("DEL", key)
		}
	})
}

func TestLargeScaleListCommands(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large-scale test in short mode")
	}
	proxy := NewTestProxy(t, sharedCluster)

	t.Run(fmt.Sprintf("LPUSH_%d_keys_clients_%d", lsNumKeys, lsNumClients), func(t *testing.T) {
		spawnClients(t, proxy, lsNumClients, func(c *RedisConn, idx int) {
			for n := 0; n < lsNumKeys; n++ {
				key := fmt.Sprintf("mylists:%d:%d:\x00\r\n:mylists", n, idx)
				val := fmt.Sprintf("%d:\x00\r\n:%d", n, n)
				c.SendBinary([]byte("LPUSH"), []byte(key), []byte(val))
				reply := c.ReadReply()
				if replyIsError(reply) {
					t.Errorf("client %d LPUSH key %d: %s", idx, n, reply)
					return
				}
			}
		})
	})

	t.Run(fmt.Sprintf("LLEN_%d_keys_clients_%d", lsNumKeys, lsNumClients), func(t *testing.T) {
		spawnClients(t, proxy, lsNumClients, func(c *RedisConn, idx int) {
			for n := 0; n < lsNumKeys; n++ {
				key := fmt.Sprintf("mylists:%d:%d:\x00\r\n:mylists", n, idx)
				reply := c.Do("LLEN", key)
				if replyIsError(reply) {
					t.Errorf("client %d LLEN key %d: %s", idx, n, reply)
					return
				}
			}
		})
	})

	t.Run(fmt.Sprintf("LINDEX_%d_keys_clients_%d", lsNumKeys, lsNumClients), func(t *testing.T) {
		spawnClients(t, proxy, lsNumClients, func(c *RedisConn, idx int) {
			for n := 0; n < lsNumKeys; n++ {
				key := fmt.Sprintf("mylists:%d:%d:\x00\r\n:mylists", n, idx)
				val := fmt.Sprintf("%d:\x00\r\n:%d", n, n)
				c.SendBinary([]byte("LINDEX"), []byte(key), []byte("0"))
				reply := c.ReadReply()
				want := "$" + strconv.Itoa(len(val)) + ":" + val
				if reply != want {
					t.Errorf("client %d LINDEX key %d: expected %q, got %q", idx, n, want, reply)
					return
				}
			}
		})
	})

	t.Run(fmt.Sprintf("LPOP_%d_keys_clients_%d", lsNumKeys, lsNumClients), func(t *testing.T) {
		spawnClients(t, proxy, lsNumClients, func(c *RedisConn, idx int) {
			for n := 0; n < lsNumKeys; n++ {
				key := fmt.Sprintf("mylists:%d:%d:\x00\r\n:mylists", n, idx)
				val := fmt.Sprintf("%d:\x00\r\n:%d", n, n)
				c.SendBinary([]byte("LPOP"), []byte(key))
				reply := c.ReadReply()
				want := "$" + strconv.Itoa(len(val)) + ":" + val
				if reply != want {
					t.Errorf("client %d LPOP key %d: expected %q, got %q", idx, n, want, reply)
					return
				}
			}
		})
	})

	spawnClients(t, proxy, 1, func(c *RedisConn, idx int) {
		for n := 0; n < lsNumKeys; n++ {
			for ci := 0; ci < lsNumClients; ci++ {
				key := fmt.Sprintf("mylists:%d:%d:\x00\r\n:mylists", n, ci)
				c.Do("DEL", key)
			}
		}
	})
}

func TestLargeScaleSetCommands(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large-scale test in short mode")
	}
	proxy := NewTestProxy(t, sharedCluster)

	t.Run(fmt.Sprintf("SADD_%d_keys_clients_%d", lsNumLists, lsNumClients), func(t *testing.T) {
		spawnClients(t, proxy, 1, func(c *RedisConn, idx int) {
			for n := 0; n < lsNumLists; n++ {
				for vn := 0; vn < 10; vn++ {
					key := fmt.Sprintf("myset:%d:\x00\r\n:myset", n)
					val := fmt.Sprintf("%d:%d:\x00\r\n:%d:%d", n, vn, n, vn)
					c.SendBinary([]byte("SADD"), []byte(key), []byte(val))
					reply := c.ReadReply()
					if replyIsError(reply) {
						t.Errorf("SADD key %d val %d: %s", n, vn, reply)
						return
					}
				}
			}
		})
	})

	t.Run(fmt.Sprintf("SMEMBERS_%d_keys_clients_%d", lsNumLists, lsNumClients), func(t *testing.T) {
		spawnClients(t, proxy, lsNumClients, func(c *RedisConn, idx int) {
			for n := 0; n < lsNumLists; n++ {
				key := fmt.Sprintf("myset:%d:\x00\r\n:myset", n)
				reply := c.Do("SMEMBERS", key)
				if replyIsError(reply) {
					t.Errorf("client %d SMEMBERS key %d: %s", idx, n, reply)
					return
				}
				if !strings.HasPrefix(reply, "*10:") {
					t.Errorf("client %d SMEMBERS key %d: expected *10:, got %s", idx, n, reply)
					return
				}
			}
		})
	})

	t.Run(fmt.Sprintf("SISMEMBER_%d_keys_clients_%d", lsNumLists, lsNumClients), func(t *testing.T) {
		spawnClients(t, proxy, lsNumClients, func(c *RedisConn, idx int) {
			for n := 0; n < lsNumLists; n++ {
				key := fmt.Sprintf("myset:%d:\x00\r\n:myset", n)
				val := fmt.Sprintf("%d:%d:\x00\r\n:%d:%d", n, 0, n, 0)
				c.SendBinary([]byte("SISMEMBER"), []byte(key), []byte(val))
				reply := c.ReadReply()
				if reply != ":1" {
					t.Errorf("client %d SISMEMBER key %d: expected :1, got %s", idx, n, reply)
					return
				}
			}
		})
	})

	spawnClients(t, proxy, 1, func(c *RedisConn, idx int) {
		for n := 0; n < lsNumLists; n++ {
			key := fmt.Sprintf("myset:%d:\x00\r\n:myset", n)
			c.Do("DEL", key)
		}
	})
}

func TestLargeScaleZSetCommands(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large-scale test in short mode")
	}
	proxy := NewTestProxy(t, sharedCluster)

	t.Run(fmt.Sprintf("ZADD_%d_keys_clients_%d", lsNumLists, lsNumClients), func(t *testing.T) {
		spawnClients(t, proxy, 1, func(c *RedisConn, idx int) {
			for n := 0; n < lsNumLists; n++ {
				for vn := 0; vn < 10; vn++ {
					key := fmt.Sprintf("myzset:%d:\x00\r\n:myzset", n)
					score := strconv.Itoa(vn)
					member := fmt.Sprintf("%d:%d:\x00\r\n:%d:%d", n, vn, n, vn)
					c.SendBinary([]byte("ZADD"), []byte(key), []byte(score), []byte(member))
					reply := c.ReadReply()
					if replyIsError(reply) {
						t.Errorf("ZADD key %d score %d: %s", n, vn, reply)
						return
					}
				}
			}
		})
	})

	t.Run(fmt.Sprintf("ZRANGE_%d_keys_clients_%d", lsNumLists, lsNumClients), func(t *testing.T) {
		spawnClients(t, proxy, lsNumClients, func(c *RedisConn, idx int) {
			for n := 0; n < lsNumLists; n++ {
				key := fmt.Sprintf("myzset:%d:\x00\r\n:myzset", n)
				reply := c.Do("ZRANGE", key, "0", "-1")
				if replyIsError(reply) {
					t.Errorf("client %d ZRANGE key %d: %s", idx, n, reply)
					return
				}
				if !strings.HasPrefix(reply, "*10:") {
					t.Errorf("client %d ZRANGE key %d: expected *10:, got %s", idx, n, reply)
					return
				}
			}
		})
	})

	t.Run(fmt.Sprintf("ZSCORE_%d_keys_clients_%d", lsNumLists, lsNumClients), func(t *testing.T) {
		spawnClients(t, proxy, lsNumClients, func(c *RedisConn, idx int) {
			for n := 0; n < lsNumLists; n++ {
				key := fmt.Sprintf("myzset:%d:\x00\r\n:myzset", n)
				member := fmt.Sprintf("%d:%d:\x00\r\n:%d:%d", n, 5, n, 5)
				c.SendBinary([]byte("ZSCORE"), []byte(key), []byte(member))
				reply := c.ReadReply()
				if replyIsError(reply) {
					t.Errorf("client %d ZSCORE key %d: %s", idx, n, reply)
					return
				}
				if !strings.Contains(reply, "5") {
					t.Errorf("client %d ZSCORE key %d: expected score 5, got %s", idx, n, reply)
					return
				}
			}
		})
	})

	spawnClients(t, proxy, 1, func(c *RedisConn, idx int) {
		for n := 0; n < lsNumLists; n++ {
			key := fmt.Sprintf("myzset:%d:\x00\r\n:myzset", n)
			c.Do("DEL", key)
		}
	})
}

func TestLargeScaleLuaCommands(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large-scale test in short mode")
	}
	proxy := NewTestProxy(t, sharedCluster)

	t.Run(fmt.Sprintf("EVAL_%d_keys_clients_%d", lsNumKeys, lsNumClients), func(t *testing.T) {
		spawnClients(t, proxy, lsNumClients, func(c *RedisConn, idx int) {
			for n := 0; n < lsNumKeys; n++ {
				key := fmt.Sprintf("mylua:%d:\x00\r\n:mylua", n)
				argv := fmt.Sprintf("argv:%d:\x00\r\n:%d", n, n)
				c.SendBinary(
					[]byte("EVAL"),
					[]byte("return KEYS[1]"),
					[]byte("1"),
					[]byte(key),
					[]byte(argv),
				)
				reply := c.ReadReply()
				want := "$" + strconv.Itoa(len(key)) + ":" + key
				if reply != want {
					t.Errorf("client %d EVAL key %d: expected %q, got %q", idx, n, want, reply)
					return
				}
			}
		})
	})

	t.Run(fmt.Sprintf("EVAL_multi_keys_crossslot_clients_%d", lsNumClients), func(t *testing.T) {
		spawnClients(t, proxy, lsNumClients, func(c *RedisConn, idx int) {
			for n := 0; n < 50; n++ {
				key1 := fmt.Sprintf("key1:%d:\x00\r\n:key1", n)
				key2 := fmt.Sprintf("key2:%d:\x00\r\n:key2", n)
				c.SendBinary(
					[]byte("EVAL"),
					[]byte("return KEYS[1] .. KEYS[2]"),
					[]byte("2"),
					[]byte(key1),
					[]byte(key2),
				)
				reply := c.ReadReply()
				if !replyIsError(reply) {
					t.Errorf("client %d EVAL multi-key cross-slot %d: expected error, got %s", idx, n, reply)
					return
				}
				if !strings.Contains(reply, "CROSSSLOT") {
					t.Errorf("client %d EVAL multi-key %d: expected CROSSSLOT, got %s", idx, n, reply)
					return
				}
			}
		})
	})
}

func TestLargeScalePipelineCommands(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large-scale test in short mode")
	}
	proxy := NewTestProxy(t, sharedCluster)

	for _, pipelineSize := range []int{2, 4} {
		pipelineSize := pipelineSize
		for _, dataLen := range []int{1, 64} {
			dataLen := dataLen
			t.Run(fmt.Sprintf("SET_pipeline_%d_size_%d_keys_%d_clients_%d", pipelineSize, dataLen, lsNumKeys, lsNumClients), func(t *testing.T) {
				spawnClients(t, proxy, lsNumClients, func(c *RedisConn, idx int) {
					batch := make([][]string, 0, pipelineSize)
					for n := 0; n < lsNumKeys; n++ {
						key := fmt.Sprintf("{pipe}:k:%d", n)
						val := strings.Repeat(strconv.Itoa(n), dataLen/len(strconv.Itoa(n))+1)[:dataLen]
						batch = append(batch, []string{key, val})
						if len(batch) == pipelineSize || n == lsNumKeys-1 {
							for _, kv := range batch {
								c.Send("SET", kv[0], kv[1])
							}
							for range batch {
								reply := c.ReadReply()
								if reply != "+OK" {
									t.Errorf("client %d pipeline SET: expected +OK, got %s", idx, reply)
									return
								}
							}
							batch = batch[:0]
						}
					}
				})
			})

			t.Run(fmt.Sprintf("GET_pipeline_%d_size_%d_keys_%d_clients_%d", pipelineSize, dataLen, lsNumKeys, lsNumClients), func(t *testing.T) {
				spawnClients(t, proxy, lsNumClients, func(c *RedisConn, idx int) {
					type kv struct{ key, val string }
					batch := make([]kv, 0, pipelineSize)
					for n := 0; n < lsNumKeys; n++ {
						key := fmt.Sprintf("{pipe}:k:%d", n)
						val := strings.Repeat(strconv.Itoa(n), dataLen/len(strconv.Itoa(n))+1)[:dataLen]
						batch = append(batch, kv{key, val})
						if len(batch) == pipelineSize || n == lsNumKeys-1 {
							for _, item := range batch {
								c.Send("GET", item.key)
							}
							for _, item := range batch {
								reply := c.ReadReply()
								want := "$" + strconv.Itoa(len(item.val)) + ":" + item.val
								if reply != want {
									t.Errorf("client %d pipeline GET %s: expected %q, got %q", idx, item.key, want, reply)
									return
								}
							}
							batch = batch[:0]
						}
					}
				})
			})
		}
	}

	spawnClients(t, proxy, 1, func(c *RedisConn, idx int) {
		for n := 0; n < lsNumKeys; n++ {
			c.Do("DEL", fmt.Sprintf("{pipe}:k:%d", n))
		}
	})
}

func TestLargeScaleMultiExec(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large-scale test in short mode")
	}
	proxy := NewTestProxy(t, sharedCluster)

	for _, dataLen := range []int{1, 64} {
		dataLen := dataLen
		t.Run(fmt.Sprintf("MULTI_SET_EXEC_size_%d_keys_%d_clients_%d", dataLen, 100, lsNumClients), func(t *testing.T) {
			spawnClients(t, proxy, lsNumClients, func(c *RedisConn, idx int) {
				for n := 0; n < 100; n++ {
					val := strings.Repeat(strconv.Itoa(n), dataLen/len(strconv.Itoa(n))+1)[:dataLen]
					count := (n%3 + 1)
					c.Send("MULTI")
					if r := c.ReadReply(); r != "+OK" {
						t.Errorf("client %d MULTI: expected +OK, got %s", idx, r)
						return
					}
					for i := 0; i < count; i++ {
						c.Send("SET", fmt.Sprintf("{tx}:k:%d", n), val)
						if r := c.ReadReply(); r != "+QUEUED" {
							t.Errorf("client %d QUEUED: expected +QUEUED, got %s", idx, r)
							return
						}
					}
					reply := c.Do("EXEC")
					if replyIsError(reply) {
						t.Errorf("client %d EXEC n=%d: unexpected error %s", idx, n, reply)
						return
					}
					if !strings.HasPrefix(reply, fmt.Sprintf("*%d:", count)) {
						t.Errorf("client %d EXEC n=%d: expected *%d:, got %s", idx, n, count, reply)
						return
					}
				}
			})
		})

		t.Run(fmt.Sprintf("MULTI_GET_EXEC_size_%d_keys_%d_clients_%d", dataLen, 100, lsNumClients), func(t *testing.T) {
			spawnClients(t, proxy, lsNumClients, func(c *RedisConn, idx int) {
				for n := 0; n < 100; n++ {
					val := strings.Repeat(strconv.Itoa(n), dataLen/len(strconv.Itoa(n))+1)[:dataLen]
					count := (n%3 + 1)
					c.Send("MULTI")
					if r := c.ReadReply(); r != "+OK" {
						t.Errorf("client %d MULTI: expected +OK, got %s", idx, r)
						return
					}
					for i := 0; i < count; i++ {
						c.Send("GET", fmt.Sprintf("{tx}:k:%d", n))
						if r := c.ReadReply(); r != "+QUEUED" {
							t.Errorf("client %d QUEUED: expected +QUEUED, got %s", idx, r)
							return
						}
					}
					reply := c.Do("EXEC")
					if replyIsError(reply) {
						t.Errorf("client %d EXEC GET n=%d: unexpected error %s", idx, n, reply)
						return
					}
					if !strings.HasPrefix(reply, fmt.Sprintf("*%d:", count)) {
						t.Errorf("client %d EXEC GET n=%d: expected *%d:, got %s", idx, n, count, reply)
						return
					}
					want := "$" + strconv.Itoa(len(val)) + ":" + val
					if !strings.Contains(reply, want) {
						t.Errorf("client %d EXEC GET n=%d: expected val %q in %s", idx, n, want, reply)
						return
					}
				}
			})
		})
	}

	spawnClients(t, proxy, 1, func(c *RedisConn, idx int) {
		for n := 0; n < 100; n++ {
			c.Do("DEL", fmt.Sprintf("{tx}:k:%d", n))
		}
	})
}

func TestLargeScaleRPUSHLRANGE(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large-scale test in short mode")
	}
	proxy := NewTestProxy(t, sharedCluster)

	for _, dataLen := range []int{1, 64} {
		dataLen := dataLen
		t.Run(fmt.Sprintf("RPUSH_%d_keys_size_%d_clients_%d", lsNumLists, dataLen, lsNumClients), func(t *testing.T) {
			spawnClients(t, proxy, 1, func(c *RedisConn, idx int) {
				for n := 0; n < lsNumLists; n++ {
					for vn := 0; vn < 10; vn++ {
						key := fmt.Sprintf("mylist:%d:%d:\x00\r\n:mylist", n, dataLen)
						val := fmt.Sprintf("%d:%d:\x00\r\n:%d:%d", n, vn, n, vn)
						c.SendBinary([]byte("RPUSH"), []byte(key), []byte(val))
						reply := c.ReadReply()
						if replyIsError(reply) {
							t.Errorf("RPUSH key %d val %d: %s", n, vn, reply)
							return
						}
					}
				}
			})
		})

		t.Run(fmt.Sprintf("LRANGE_%d_keys_size_%d_clients_%d", lsNumLists, dataLen, lsNumClients), func(t *testing.T) {
			spawnClients(t, proxy, lsNumClients, func(c *RedisConn, idx int) {
				for n := 0; n < lsNumLists; n++ {
					key := fmt.Sprintf("mylist:%d:%d:\x00\r\n:mylist", n, dataLen)
					reply := c.Do("LRANGE", key, "0", "-1")
					if replyIsError(reply) {
						t.Errorf("client %d LRANGE key %d: %s", idx, n, reply)
						return
					}
					if !strings.HasPrefix(reply, "*10:") {
						t.Errorf("client %d LRANGE key %d: expected *10:, got %s", idx, n, reply)
						return
					}
					for vn := 0; vn < 10; vn++ {
						val := fmt.Sprintf("%d:%d:\x00\r\n:%d:%d", n, vn, n, vn)
						if !strings.Contains(reply, val) {
							t.Errorf("client %d LRANGE key %d: missing val %d: %q", idx, n, vn, val)
							return
						}
					}
				}
			})
		})
	}

	spawnClients(t, proxy, 1, func(c *RedisConn, idx int) {
		for n := 0; n < lsNumLists; n++ {
			for _, dataLen := range []int{1, 64} {
				key := fmt.Sprintf("mylist:%d:%d:\x00\r\n:mylist", n, dataLen)
				c.Do("DEL", key)
			}
		}
	})
}

func TestLargeScaleGeoCommands(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large-scale test in short mode")
	}
	proxy := NewTestProxy(t, sharedCluster)
	numKeys := lsNumLists

	t.Run(fmt.Sprintf("GEOADD_%d_keys_clients_%d", numKeys, lsNumClients), func(t *testing.T) {
		spawnClients(t, proxy, lsNumClients, func(c *RedisConn, idx int) {
			for n := 0; n < numKeys; n++ {
				key := fmt.Sprintf("geo:%d:\x00\r\n:geo", n)
				member1 := "Palermo\x00\r\nPalermo"
				member2 := "Catania\x00\r\nCatania"
				c.SendBinary([]byte("GEOADD"), []byte(key),
					[]byte("13.361389"), []byte("38.115556"), []byte(member1),
					[]byte("15.087269"), []byte("37.502669"), []byte(member2))
				reply := c.ReadReply()
				if replyIsError(reply) {
					t.Errorf("client %d GEOADD key %d: unexpected error %s", idx, n, reply)
					return
				}
			}
		})
	})

	t.Run(fmt.Sprintf("GEOHASH_%d_keys_clients_%d", numKeys, lsNumClients), func(t *testing.T) {
		spawnClients(t, proxy, lsNumClients, func(c *RedisConn, idx int) {
			for n := 0; n < numKeys; n++ {
				key := fmt.Sprintf("geo:%d:\x00\r\n:geo", n)
				member1 := "Palermo\x00\r\nPalermo"
				member2 := "Catania\x00\r\nCatania"
				c.SendBinary([]byte("GEOHASH"), []byte(key), []byte(member1), []byte(member2))
				reply := c.ReadReply()
				if replyIsError(reply) {
					t.Errorf("client %d GEOHASH key %d: unexpected error %s", idx, n, reply)
					return
				}
				if !strings.HasPrefix(reply, "*2:") {
					t.Errorf("client %d GEOHASH key %d: expected 2-element array, got %s", idx, n, reply)
				}
			}
		})
	})

	t.Run(fmt.Sprintf("GEOPOS_%d_keys_clients_%d", numKeys, lsNumClients), func(t *testing.T) {
		spawnClients(t, proxy, lsNumClients, func(c *RedisConn, idx int) {
			for n := 0; n < numKeys; n++ {
				key := fmt.Sprintf("geo:%d:\x00\r\n:geo", n)
				member1 := "Palermo\x00\r\nPalermo"
				member2 := "Catania\x00\r\nCatania"
				c.SendBinary([]byte("GEOPOS"), []byte(key), []byte(member1), []byte(member2))
				reply := c.ReadReply()
				if replyIsError(reply) {
					t.Errorf("client %d GEOPOS key %d: unexpected error %s", idx, n, reply)
					return
				}
				if !strings.HasPrefix(reply, "*2:") {
					t.Errorf("client %d GEOPOS key %d: expected 2-element array, got %s", idx, n, reply)
				}
			}
		})
	})

	t.Run(fmt.Sprintf("GEODIST_%d_keys_clients_%d", numKeys, lsNumClients), func(t *testing.T) {
		spawnClients(t, proxy, lsNumClients, func(c *RedisConn, idx int) {
			for n := 0; n < numKeys; n++ {
				key := fmt.Sprintf("geo:%d:\x00\r\n:geo", n)
				member1 := "Palermo\x00\r\nPalermo"
				member2 := "Catania\x00\r\nCatania"
				c.SendBinary([]byte("GEODIST"), []byte(key), []byte(member1), []byte(member2), []byte("km"))
				reply := c.ReadReply()
				if replyIsError(reply) {
					t.Errorf("client %d GEODIST key %d: unexpected error %s", idx, n, reply)
					return
				}
				if replyIsNil(reply) {
					t.Errorf("client %d GEODIST key %d: expected distance, got nil", idx, n)
				}
			}
		})
	})

	t.Run(fmt.Sprintf("GEOSEARCH_%d_keys_clients_%d", numKeys, lsNumClients), func(t *testing.T) {
		spawnClients(t, proxy, lsNumClients, func(c *RedisConn, idx int) {
			for n := 0; n < numKeys; n++ {
				key := fmt.Sprintf("geo:%d:\x00\r\n:geo", n)
				c.SendBinary([]byte("GEOSEARCH"), []byte(key),
					[]byte("FROMMEMBER"), []byte("Palermo\x00\r\nPalermo"),
					[]byte("BYRADIUS"), []byte("1000"), []byte("km"),
					[]byte("ASC"))
				reply := c.ReadReply()
				if replyIsError(reply) {
					t.Errorf("client %d GEOSEARCH key %d: unexpected error %s", idx, n, reply)
				}
			}
		})
	})
}

func TestLargeScaleHyperLogLogCommands(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large-scale test in short mode")
	}
	proxy := NewTestProxy(t, sharedCluster)
	numKeys := lsNumKeys

	t.Run(fmt.Sprintf("PFADD_%d_keys_clients_%d", numKeys, lsNumClients), func(t *testing.T) {
		spawnClients(t, proxy, lsNumClients, func(c *RedisConn, idx int) {
			for n := 0; n < numKeys; n++ {
				key := fmt.Sprintf("pf:%d:\x00\r\n:pf", n)
				val := fmt.Sprintf("%d:\x00\r\n:%d", n, n)
				c.SendBinary([]byte("PFADD"), []byte(key), []byte(val))
				reply := c.ReadReply()
				if replyIsError(reply) {
					t.Errorf("client %d PFADD key %d: unexpected error %s", idx, n, reply)
					return
				}
			}
		})
	})

	t.Run(fmt.Sprintf("PFCOUNT_%d_keys_clients_%d", numKeys, lsNumClients), func(t *testing.T) {
		spawnClients(t, proxy, lsNumClients, func(c *RedisConn, idx int) {
			for n := 0; n < numKeys; n++ {
				key := fmt.Sprintf("pf:%d:\x00\r\n:pf", n)
				c.SendBinary([]byte("PFCOUNT"), []byte(key))
				reply := c.ReadReply()
				if replyIsError(reply) {
					t.Errorf("client %d PFCOUNT key %d: unexpected error %s", idx, n, reply)
					return
				}
				count := replyInt(t, reply)
				if count != 1 {
					t.Errorf("client %d PFCOUNT key %d: expected 1, got %d", idx, n, count)
				}
			}
		})
	})
}

func TestLargeScaleCrossSlotMGET(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large-scale test in short mode")
	}
	proxy := NewTestProxy(t, sharedCluster)

	numKeys := 200
	keys := make([]string, numKeys)
	vals := make([]string, numKeys)
	for i := 0; i < numKeys; i++ {
		keys[i] = fmt.Sprintf("xslot:k:%d", i)
		vals[i] = fmt.Sprintf("v%d:\x00\r\n:v%d", i, i)
	}

	spawnClients(t, proxy, 1, func(c *RedisConn, idx int) {
		for i := 0; i < numKeys; i++ {
			c.SendBinary([]byte("SET"), []byte(keys[i]), []byte(vals[i]))
			reply := c.ReadReply()
			if reply != "+OK" {
				t.Errorf("SET key %d: expected +OK, got %s", i, reply)
				return
			}
		}
	})

	spawnClients(t, proxy, lsNumClients, func(c *RedisConn, idx int) {
		args := make([][]byte, numKeys+1)
		args[0] = []byte("MGET")
		for i := 0; i < numKeys; i++ {
			args[i+1] = []byte(keys[i])
		}
		c.SendBinary(args...)
		reply := c.ReadReply()
		if !strings.HasPrefix(reply, fmt.Sprintf("*%d:", numKeys)) {
			t.Errorf("client %d cross-slot MGET: expected *%d:, got %s", idx, numKeys, reply)
			return
		}
		for i := 0; i < numKeys; i++ {
			if !strings.Contains(reply, vals[i]) {
				t.Errorf("client %d cross-slot MGET: missing val %d", idx, i)
				return
			}
		}
	})

	spawnClients(t, proxy, 1, func(c *RedisConn, idx int) {
		for i := 0; i < numKeys; i++ {
			c.Do("DEL", keys[i])
		}
	})
}
