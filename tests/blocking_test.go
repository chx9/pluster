package integration

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func readBlockReply(c *RedisConn, timeout time.Duration) string {
	c.conn.SetReadDeadline(time.Now().Add(timeout))
	defer c.conn.SetReadDeadline(time.Time{})
	return c.ReadReply()
}

func TestBLPOPBasic(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	blocker := DialProxy(t, proxy)
	pusher := DialProxy(t, proxy)

	key := "{blpop}:basic"
	blocker.Do("DEL", key)

	result := make(chan string, 1)
	go func() {
		blocker.Send("BLPOP", key, "5")
		result <- readBlockReply(blocker, 6*time.Second)
	}()

	time.Sleep(150 * time.Millisecond)
	pusher.Do("RPUSH", key, "hello")

	reply := <-result
	if !strings.Contains(reply, "hello") {
		t.Errorf("BLPOP: expected reply containing 'hello', got %s", reply)
	}
	if !strings.Contains(reply, key) {
		t.Errorf("BLPOP: expected reply containing key %q, got %s", key, reply)
	}
	blocker.Do("DEL", key)
}

func TestBLPOPTimeout(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	key := "{blpop}:timeout"
	c.Do("DEL", key)

	start := time.Now()
	c.Send("BLPOP", key, "1")
	reply := readBlockReply(c, 4*time.Second)
	elapsed := time.Since(start)

	if reply != "*-1" && !strings.HasPrefix(reply, "*0") {
		t.Errorf("BLPOP timeout: expected nil/empty reply, got %q", reply)
	}
	if elapsed < 800*time.Millisecond {
		t.Errorf("BLPOP timeout: returned too fast (%v), expected ~1s", elapsed)
	}
	if elapsed > 4*time.Second {
		t.Errorf("BLPOP timeout: took too long (%v)", elapsed)
	}
}

func TestBRPOPBasic(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	blocker := DialProxy(t, proxy)
	pusher := DialProxy(t, proxy)

	key := "{brpop}:basic"
	blocker.Do("DEL", key)

	result := make(chan string, 1)
	go func() {
		blocker.Send("BRPOP", key, "5")
		result <- readBlockReply(blocker, 6*time.Second)
	}()

	time.Sleep(150 * time.Millisecond)
	pusher.Do("LPUSH", key, "world")

	reply := <-result
	if !strings.Contains(reply, "world") {
		t.Errorf("BRPOP: expected reply containing 'world', got %s", reply)
	}
	blocker.Do("DEL", key)
}

func TestBLPOPPipelineAfterUnblock(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	blocker := DialProxy(t, proxy)
	pusher := DialProxy(t, proxy)

	key := "{blpop}:pipeline"
	getKey := "{blpop}:after"
	blocker.Do("DEL", key)
	blocker.Do("SET", getKey, "afterval")

	results := make(chan []string, 1)
	go func() {
		blocker.Send("BLPOP", key, "5")
		blocker.Send("GET", getKey)
		replies := []string{
			readBlockReply(blocker, 6*time.Second),
			readBlockReply(blocker, 3*time.Second),
		}
		results <- replies
	}()

	time.Sleep(150 * time.Millisecond)
	pusher.Do("RPUSH", key, "val")

	replies := <-results
	if !strings.Contains(replies[0], "val") {
		t.Errorf("BLPOP pipeline: first reply should contain 'val', got %s", replies[0])
	}
	if !strings.Contains(replies[1], "afterval") {
		t.Errorf("GET after BLPOP: expected 'afterval', got %s", replies[1])
	}
	blocker.Do("DEL", key, getKey)
}

func TestBZPOPMINBasic(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)
	pusher := DialProxy(t, proxy)

	key := "{bzpop}:zset"
	c.Do("DEL", key)

	result := make(chan string, 1)
	go func() {
		c.Send("BZPOPMIN", key, "5")
		result <- readBlockReply(c, 6*time.Second)
	}()

	time.Sleep(150 * time.Millisecond)
	pusher.Do("ZADD", key, "1.5", "member1")

	reply := <-result
	if !strings.Contains(reply, "member1") {
		t.Errorf("BZPOPMIN: expected 'member1' in reply, got %s", reply)
	}
	c.Do("DEL", key)
}

func TestBLPOPMultipleClients(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	n := 5
	var wg sync.WaitGroup
	errCh := make(chan error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			blocker := DialProxy(t, proxy)
			pusher := DialProxy(t, proxy)
			key := fmt.Sprintf("{blpop:mc}:key:%d", id)
			blocker.Do("DEL", key)

			result := make(chan string, 1)
			go func() {
				blocker.Send("BLPOP", key, "5")
				result <- readBlockReply(blocker, 6*time.Second)
			}()

			time.Sleep(100 * time.Millisecond)
			pusher.Do("RPUSH", key, fmt.Sprintf("val%d", id))

			reply := <-result
			if !strings.Contains(reply, fmt.Sprintf("val%d", id)) {
				errCh <- fmt.Errorf("client %d: expected val%d, got %s", id, id, reply)
			}
			blocker.Do("DEL", key)
		}(i)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

func TestBLPOPZeroTimeout(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)
	pusher := DialProxy(t, proxy)

	key := "{blpop}:zero"
	c.Do("DEL", key)

	result := make(chan string, 1)
	go func() {
		c.Send("BLPOP", key, "0")
		result <- readBlockReply(c, 10*time.Second)
	}()

	time.Sleep(200 * time.Millisecond)
	pusher.Do("RPUSH", key, "zerodata")

	reply := <-result
	if !strings.Contains(reply, "zerodata") {
		t.Errorf("BLPOP zero timeout: expected 'zerodata', got %s", reply)
	}
	c.Do("DEL", key)
}

func TestBLPOPConnPoolReuse(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	blocker := DialProxy(t, proxy)
	pusher := DialProxy(t, proxy)

	key := "{blpop}:pool"
	blocker.Do("DEL", key)

	const rounds = 5
	for i := 0; i < rounds; i++ {
		result := make(chan string, 1)
		go func() {
			blocker.Send("BLPOP", key, "5")
			result <- readBlockReply(blocker, 6*time.Second)
		}()

		time.Sleep(50 * time.Millisecond)
		pusher.Do("RPUSH", key, fmt.Sprintf("val%d", i))

		reply := <-result
		if !strings.Contains(reply, fmt.Sprintf("val%d", i)) {
			t.Errorf("round %d: expected val%d, got %s", i, i, reply)
		}
	}
	blocker.Do("DEL", key)

	reuses := proxy.BlockingPoolReuses()
	if reuses < rounds-1 {
		t.Errorf("expected at least %d connection reuses after %d rounds, got %d", rounds-1, rounds, reuses)
	}
}

func TestBLPOPConnPoolCapacity(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	const n = 12
	var wg sync.WaitGroup
	errCh := make(chan error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			blocker := DialProxy(t, proxy)
			pusher := DialProxy(t, proxy)
			key := fmt.Sprintf("{blpop:cap}:key:%d", id)
			blocker.Do("DEL", key)

			result := make(chan string, 1)
			go func() {
				blocker.Send("BLPOP", key, "5")
				result <- readBlockReply(blocker, 6*time.Second)
			}()

			time.Sleep(80 * time.Millisecond)
			pusher.Do("RPUSH", key, fmt.Sprintf("capval%d", id))

			reply := <-result
			if !strings.Contains(reply, fmt.Sprintf("capval%d", id)) {
				errCh <- fmt.Errorf("client %d: expected capval%d, got %s", id, id, reply)
			}
			blocker.Do("DEL", key)
		}(i)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}
