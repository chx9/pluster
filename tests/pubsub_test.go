package integration

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func readSubReply(c *RedisConn, timeout time.Duration) string {
	c.conn.SetReadDeadline(time.Now().Add(timeout))
	return c.ReadReply()
}

func TestPubSubSubscribeUnsubscribe(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	sub := DialProxy(t, proxy)
	pub := DialProxy(t, proxy)

	sub.Send("SUBSCRIBE", "chan1")
	reply := readSubReply(sub, 3*time.Second)
	if !strings.HasPrefix(reply, "*3:") {
		t.Fatalf("SUBSCRIBE: expected array of 3, got %s", reply)
	}
	if !strings.Contains(reply, "subscribe") {
		t.Errorf("SUBSCRIBE: expected 'subscribe' in reply, got %s", reply)
	}
	if !strings.Contains(reply, "chan1") {
		t.Errorf("SUBSCRIBE: expected channel name in reply, got %s", reply)
	}

	pub.Send("PUBLISH", "chan1", "hello")
	pubReply := pub.ReadReply()
	if !strings.HasPrefix(pubReply, ":") {
		t.Errorf("PUBLISH: expected integer reply, got %s", pubReply)
	}

	msg := readSubReply(sub, 3*time.Second)
	if !strings.Contains(msg, "message") {
		t.Errorf("received message: expected 'message' type, got %s", msg)
	}
	if !strings.Contains(msg, "hello") {
		t.Errorf("received message: expected 'hello', got %s", msg)
	}

	sub.Send("UNSUBSCRIBE", "chan1")
	unsubReply := readSubReply(sub, 3*time.Second)
	if !strings.HasPrefix(unsubReply, "*3:") {
		t.Errorf("UNSUBSCRIBE: expected array of 3, got %s", unsubReply)
	}
	if !strings.Contains(unsubReply, "unsubscribe") {
		t.Errorf("UNSUBSCRIBE: expected 'unsubscribe' in reply, got %s", unsubReply)
	}
}

func TestPubSubMultipleChannels(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	sub := DialProxy(t, proxy)
	pub := DialProxy(t, proxy)

	channels := []string{"mc:ch1", "mc:ch2", "mc:ch3"}
	for _, ch := range channels {
		sub.Send("SUBSCRIBE", ch)
		reply := readSubReply(sub, 3*time.Second)
		if !strings.Contains(reply, "subscribe") {
			t.Fatalf("SUBSCRIBE %s: expected subscribe, got %s", ch, reply)
		}
	}

	pub.Send("PUBLISH", "mc:ch2", "msg-for-ch2")
	pub.ReadReply()

	msg := readSubReply(sub, 3*time.Second)
	if !strings.Contains(msg, "msg-for-ch2") {
		t.Errorf("expected msg-for-ch2, got %s", msg)
	}
	if !strings.Contains(msg, "mc:ch2") {
		t.Errorf("expected channel mc:ch2 in message, got %s", msg)
	}

	for _, ch := range channels {
		sub.Send("UNSUBSCRIBE", ch)
		readSubReply(sub, 2*time.Second)
	}
}

func TestPubSubPatternSubscribe(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	sub := DialProxy(t, proxy)
	pub := DialProxy(t, proxy)

	sub.Send("PSUBSCRIBE", "ptest:*")
	reply := readSubReply(sub, 3*time.Second)
	if !strings.Contains(reply, "psubscribe") {
		t.Fatalf("PSUBSCRIBE: expected psubscribe, got %s", reply)
	}

	pub.Send("PUBLISH", "ptest:news", "breaking")
	pub.ReadReply()

	msg := readSubReply(sub, 3*time.Second)
	if !strings.Contains(msg, "pmessage") {
		t.Errorf("PSUBSCRIBE message: expected pmessage type, got %s", msg)
	}
	if !strings.Contains(msg, "breaking") {
		t.Errorf("PSUBSCRIBE message: expected 'breaking', got %s", msg)
	}
	if !strings.Contains(msg, "ptest:news") {
		t.Errorf("PSUBSCRIBE message: expected channel 'ptest:news', got %s", msg)
	}

	sub.Send("PUNSUBSCRIBE", "ptest:*")
	unsubReply := readSubReply(sub, 3*time.Second)
	if !strings.Contains(unsubReply, "punsubscribe") {
		t.Errorf("PUNSUBSCRIBE: expected punsubscribe, got %s", unsubReply)
	}
}

func TestPubSubNoSubscribers(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	pub := DialProxy(t, proxy)

	reply := pub.Do("PUBLISH", "nosub:channel", "msg")
	if replyIsError(reply) {
		t.Errorf("PUBLISH to empty channel: expected integer, got %s", reply)
	}
	n := replyInt(t, reply)
	if n != 0 {
		t.Errorf("PUBLISH to empty channel: expected 0 receivers, got %d", n)
	}
}

func TestPubSubCommandsNotAllowedInSubscribeState(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	sub := DialProxy(t, proxy)

	sub.Send("SUBSCRIBE", "block:ch")
	readSubReply(sub, 3*time.Second)

	sub.Send("SET", "somekey", "val")
	reply := readSubReply(sub, 3*time.Second)
	if !replyIsError(reply) {
		t.Errorf("SET in subscribe state: expected error, got %s", reply)
	}
	if !strings.Contains(reply, "subscription") {
		t.Errorf("SET in subscribe state: expected subscription context error, got %s", reply)
	}

	sub.Send("UNSUBSCRIBE", "block:ch")
	readSubReply(sub, 2*time.Second)
}

func TestPubSubConcurrentPublishers(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	sub := DialProxy(t, proxy)

	sub.Send("SUBSCRIBE", "conc:ch")
	reply := readSubReply(sub, 3*time.Second)
	if !strings.Contains(reply, "subscribe") {
		t.Fatalf("SUBSCRIBE: expected subscribe, got %s", reply)
	}

	numPublishers := 3
	numMsgs := 3
	done := make(chan struct{})

	go func() {
		defer close(done)
		received := 0
		for received < numPublishers*numMsgs {
			msg := readSubReply(sub, 5*time.Second)
			if msg == "" || replyIsError(msg) {
				return
			}
			if strings.Contains(msg, "message") {
				received++
			}
		}
	}()

	for i := 0; i < numPublishers; i++ {
		go func(id int) {
			p := DialProxy(t, proxy)
			for j := 0; j < numMsgs; j++ {
				p.Send("PUBLISH", "conc:ch", fmt.Sprintf("msg-%d-%d", id, j))
				p.ReadReply()
			}
		}(i)
	}

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Error("concurrent publish: timed out waiting for all messages")
	}

	sub.Send("UNSUBSCRIBE", "conc:ch")
	readSubReply(sub, 2*time.Second)
}

func TestPubSubPingInSubscribeState(t *testing.T) {
	proxy := NewTestProxy(t, sharedCluster)

	sub := DialProxy(t, proxy)

	sub.Send("SUBSCRIBE", "ping:ch")
	readSubReply(sub, 3*time.Second)

	sub.Send("PING")
	reply := readSubReply(sub, 3*time.Second)
	if !strings.Contains(reply, "pong") && !strings.Contains(reply, "PONG") {
		t.Errorf("PING in subscribe state: expected pong, got %s", reply)
	}

	sub.Send("UNSUBSCRIBE", "ping:ch")
	readSubReply(sub, 2*time.Second)
}
