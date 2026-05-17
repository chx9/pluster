package engine

import (
	"testing"

	gnet "github.com/panjf2000/gnet/v2"
)

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern string
		subject string
		want    bool
	}{
		{"*", "anything", true},
		{"*", "", true},
		{"news:*", "news:sports", true},
		{"news:*", "news:", true},
		{"news:*", "other:sports", false},
		{"h?llo", "hello", true},
		{"h?llo", "hllo", false},
		{"h?llo", "heello", false},
		{"exact", "exact", true},
		{"exact", "Exact", false},
		{"[abc]oo", "aoo", true},
		{"[abc]oo", "doo", false},
	}
	for _, c := range cases {
		got := globMatch(c.pattern, c.subject)
		if got != c.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", c.pattern, c.subject, got, c.want)
		}
	}
}

type stubConn struct{ gnet.Conn }

func TestPubsubHubAddRemoveSub(t *testing.T) {
	hub := &pubsubHub{
		subs:       make(map[string][]subEntry),
		clientSubs: make(map[gnet.Conn]map[string]struct{}),
	}

	fakeConn := &stubConn{}
	hub.addSub(fakeConn, nil, "chan1", false)
	hub.addSub(fakeConn, nil, "chan2", false)

	if len(hub.subs["chan1"]) != 1 {
		t.Errorf("expected 1 entry for chan1, got %d", len(hub.subs["chan1"]))
	}
	if len(hub.clientSubs[fakeConn]) != 2 {
		t.Errorf("expected 2 client subs, got %d", len(hub.clientSubs[fakeConn]))
	}

	hub.addSub(fakeConn, nil, "chan1", false)
	if len(hub.subs["chan1"]) != 1 {
		t.Error("duplicate addSub should not create duplicate entry")
	}

	hub.removeSub(fakeConn, "chan1")
	if len(hub.subs["chan1"]) != 0 {
		t.Errorf("expected 0 entries for chan1 after remove, got %d", len(hub.subs["chan1"]))
	}
	if _, ok := hub.subs["chan1"]; ok {
		t.Error("empty slice should be deleted from subs map")
	}
	if _, ok := hub.clientSubs[fakeConn]["chan1"]; ok {
		t.Error("chan1 should be removed from clientSubs")
	}
}

func TestPubsubHubRemoveAllSubs(t *testing.T) {
	hub := &pubsubHub{
		subs:       make(map[string][]subEntry),
		clientSubs: make(map[gnet.Conn]map[string]struct{}),
	}

	conn1 := &stubConn{}
	conn2 := &stubConn{}
	hub.addSub(conn1, nil, "a", false)
	hub.addSub(conn1, nil, "b", false)
	hub.addSub(conn2, nil, "a", false)

	hub.removeAllSubs(conn1)

	if len(hub.subs["a"]) != 1 {
		t.Errorf("conn2 should still be subscribed to 'a', got %d entries", len(hub.subs["a"]))
	}
	if hub.subs["a"][0].clientConn != conn2 {
		t.Error("remaining entry for 'a' should belong to conn2")
	}
	if _, ok := hub.subs["b"]; ok {
		t.Error("'b' should be fully removed after conn1 unsubscribes")
	}
}
