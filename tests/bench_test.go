package integration

import (
	"bufio"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"
)

func dialBench(b *testing.B) *RedisConn {
	b.Helper()
	conn, err := net.DialTimeout("tcp", sharedProxy.Addr(), 5*time.Second)
	if err != nil {
		b.Fatalf("dial: %v", err)
	}
	b.Cleanup(func() { conn.Close() })
	return &RedisConn{conn: conn, r: bufio.NewReader(conn)}
}

func BenchmarkSingleKeyGET(b *testing.B) {
	c := dialBench(b)
	c.Do("SET", "{bench}:get", "hello")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Do("GET", "{bench}:get")
	}
}

func BenchmarkSingleKeySET(b *testing.B) {
	c := dialBench(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Do("SET", "{bench}:set", "hello")
	}
}

func BenchmarkPipelineDepth16(b *testing.B) {
	c := dialBench(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for p := 0; p < 16; p++ {
			c.Send("SET", fmt.Sprintf("{bench}:pipe:%d", p), "v")
		}
		for p := 0; p < 16; p++ {
			c.ReadReply()
		}
	}
}

func BenchmarkPipelineOrdering(b *testing.B) {
	c := dialBench(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("{bench}:ord:%d", i%100)
		c.Send("SET", key, "val")
		c.Send("GET", key)
		c.ReadReply()
		c.ReadReply()
	}
}

func BenchmarkMGETCrossSlot(b *testing.B) {
	c := dialBench(b)
	keys := make([]string, 10)
	for i := range keys {
		keys[i] = fmt.Sprintf("bench:mget:%d", i)
		c.Do("SET", keys[i], fmt.Sprintf("val%d", i))
	}
	args := append([]string{"MGET"}, keys...)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Do(args...)
	}
}

func BenchmarkMSETCrossSlot(b *testing.B) {
	c := dialBench(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		args := []string{"MSET"}
		for j := 0; j < 10; j++ {
			args = append(args, fmt.Sprintf("bench:mset:%d", j), fmt.Sprintf("v%d", i))
		}
		c.Do(args...)
	}
}

func BenchmarkCrossSlotFanout9Keys(b *testing.B) {
	c := dialBench(b)
	for i := 0; i < 9; i++ {
		c.Do("SET", fmt.Sprintf("bench:cross:%d", i), fmt.Sprintf("v%d", i))
	}
	args := []string{"MGET"}
	for j := 0; j < 9; j++ {
		args = append(args, fmt.Sprintf("bench:cross:%d", j))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Do(args...)
	}
}

func BenchmarkConcurrentPipeline(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		conn, err := net.DialTimeout("tcp", sharedProxy.Addr(), 5*time.Second)
		if err != nil {
			b.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		c := &RedisConn{conn: conn, r: bufio.NewReader(conn)}
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("{bench}:conc:%d", i%50)
			c.Send("SET", key, "v")
			c.Send("GET", key)
			c.ReadReply()
			c.ReadReply()
			i++
		}
	})
}

func BenchmarkConcurrentClients20(b *testing.B) {
	const numClients = 20
	conns := make([]*RedisConn, numClients)
	for i := range conns {
		conn, err := net.DialTimeout("tcp", sharedProxy.Addr(), 5*time.Second)
		if err != nil {
			b.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		conns[i] = &RedisConn{conn: conn, r: bufio.NewReader(conn)}
	}
	b.ResetTimer()
	var wg sync.WaitGroup
	opsPerClient := b.N / numClients
	if opsPerClient == 0 {
		opsPerClient = 1
	}
	for _, c := range conns {
		c := c
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerClient; i++ {
				c.Do("SET", fmt.Sprintf("{bench}:cc:%d", i%20), "v")
			}
		}()
	}
	wg.Wait()
}
