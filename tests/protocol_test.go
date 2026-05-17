package integration

import (
	"bytes"
	"fmt"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestLargeValueSetGet(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	sizes := []int{
		64 * 1024,
		512 * 1024,
		1024 * 1024,
	}

	for _, size := range sizes {
		t.Run(fmt.Sprintf("size_%d", size), func(t *testing.T) {
			key := fmt.Sprintf("large:key:%d", size)
			val := bytes.Repeat([]byte("x"), size)
			defer c.Do("DEL", key)

			c.SendBinary([]byte("SET"), []byte(key), val)
			reply := c.ReadReply()
			if reply != "+OK" {
				t.Fatalf("SET large value (%d bytes): expected +OK, got %s", size, reply)
			}

			c.SendBinary([]byte("GET"), []byte(key))
			reply = c.ReadReply()
			want := "$" + strconv.Itoa(size) + ":" + string(val)
			if reply != want {
				t.Errorf("GET large value (%d bytes): length mismatch, got len=%d", size, len(reply))
			}
		})
	}
}

func TestReplyOrderUnderConcurrentLoad(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	numClients := 5
	numOps := 30
	var wg sync.WaitGroup
	errs := make(chan string, numClients*numOps)

	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			c := DialProxy(t, proxy)
			for j := 0; j < numOps; j++ {
				key := fmt.Sprintf("{order}:c%d:k%d", clientID, j)
				val := fmt.Sprintf("v%d_%d", clientID, j)
				c.Send("SET", key, val)
			}
			for j := 0; j < numOps; j++ {
				reply := c.ReadReply()
				if reply != "+OK" {
					errs <- fmt.Sprintf("client %d op %d: expected +OK got %s", clientID, j, reply)
				}
			}
			for j := 0; j < numOps; j++ {
				key := fmt.Sprintf("{order}:c%d:k%d", clientID, j)
				c.Send("GET", key)
			}
			for j := 0; j < numOps; j++ {
				key := fmt.Sprintf("{order}:c%d:k%d", clientID, j)
				want := fmt.Sprintf("v%d_%d", clientID, j)
				reply := c.ReadReply()
				got := replyBulk(t, reply)
				if got != want {
					errs <- fmt.Sprintf("client %d GET %s: expected %s got %s", clientID, key, want, got)
				}
			}
			for j := 0; j < numOps; j++ {
				key := fmt.Sprintf("{order}:c%d:k%d", clientID, j)
				c.Do("DEL", key)
			}
		}(i)
	}

	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

func TestPipelineMixedCommands(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)
	c := DialProxy(t, proxy)

	c.MustOK(t, "SET", "{mixed}:k", "hello")
	defer c.Do("DEL", "{mixed}:k")

	c.Send("GET", "{mixed}:k")
	c.Send("PING")
	c.Send("GET", "{mixed}:k")

	r1 := c.ReadReply()
	if replyBulk(t, r1) != "hello" {
		t.Errorf("pipelined GET 1: expected hello, got %s", r1)
	}

	r2 := c.ReadReply()
	if r2 != "+PONG" {
		t.Errorf("pipelined PING: expected +PONG, got %s", r2)
	}

	r3 := c.ReadReply()
	if replyBulk(t, r3) != "hello" {
		t.Errorf("pipelined GET 2: expected hello, got %s", r3)
	}
}

func TestClientDisconnectMidRequest(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	conn, err := net.DialTimeout("tcp", proxy.Addr(), 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	fmt.Fprintf(conn, "*3\r\n$3\r\nSET\r\n$10\r\ndisconn:k1\r\n$5\r\nval1\r\n")
	buf := make([]byte, 64)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	conn.Read(buf)

	conn.Close()

	time.Sleep(100 * time.Millisecond)

	c := DialProxy(t, proxy)
	c.MustOK(t, "SET", "after:disconnect", "ok")
	c.MustGet(t, "after:disconnect", "ok")
	c.Do("DEL", "after:disconnect")
}
