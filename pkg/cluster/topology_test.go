package cluster

import (
	"sort"
	"sync"
	"testing"
)

func TestTopologyGetNodeForSlot(t *testing.T) {
	topo := NewTopology()

	node := &Node{ID: "n1", Addr: "127.0.0.1:7000", Role: RoleMaster}
	nodes := map[string]*Node{"127.0.0.1:7000": node}
	var slots [ClusterSlots]*Node
	for i := 0; i < 8192; i++ {
		slots[i] = node
	}
	topo.Update(nodes, slots, topoFingerprint(nodes))

	got := topo.GetNodeForSlot(0)
	if got == nil || got.Addr != "127.0.0.1:7000" {
		t.Errorf("expected node at slot 0, got %v", got)
	}

	got = topo.GetNodeForSlot(8192)
	if got != nil {
		t.Errorf("expected nil at slot 8192, got %v", got)
	}

	got = topo.GetNodeForSlot(-1)
	if got != nil {
		t.Errorf("expected nil for negative slot, got %v", got)
	}
}

func TestTopologyAllMasters(t *testing.T) {
	topo := NewTopology()

	n1 := &Node{ID: "n1", Addr: "127.0.0.1:7000", Role: RoleMaster}
	n2 := &Node{ID: "n2", Addr: "127.0.0.1:7001", Role: RoleMaster}
	nodes := map[string]*Node{
		"127.0.0.1:7000": n1,
		"127.0.0.1:7001": n2,
	}
	var slots [ClusterSlots]*Node
	for i := 0; i < 8192; i++ {
		slots[i] = n1
	}
	for i := 8192; i < 16384; i++ {
		slots[i] = n2
	}
	topo.Update(nodes, slots, topoFingerprint(nodes))

	masters := topo.AllMasters()
	if len(masters) != 2 {
		t.Errorf("expected 2 masters, got %d", len(masters))
	}
}

func TestTopologyVersion(t *testing.T) {
	topo := NewTopology()
	v0 := topo.Version()

	topo.Update(map[string]*Node{}, [ClusterSlots]*Node{}, "")
	v1 := topo.Version()
	if v1 <= v0 {
		t.Error("version should increase after update")
	}
}

func TestParseClusterNodes(t *testing.T) {
	raw := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa 127.0.0.1:7000@17000 myself,master - 0 1700000000000 1 connected 0-8191\n" +
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb 127.0.0.1:7001@17001 master - 0 1700000000001 2 connected 8192-16383\n" +
		"cccccccccccccccccccccccccccccccccccccccc 127.0.0.1:7003@17003 slave aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa 0 1700000000002 1 connected\n" +
		"dddddddddddddddddddddddddddddddddddddddd 127.0.0.1:7004@17004 slave bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb 0 1700000000003 2 connected\n"

	nodes, slots := parseClusterNodes(raw)

	if len(nodes) != 4 {
		t.Errorf("expected 4 nodes, got %d", len(nodes))
	}

	master0 := slots[0]
	if master0 == nil || master0.Addr != "127.0.0.1:7000" {
		t.Errorf("slot 0 should belong to 127.0.0.1:7000, got %v", master0)
	}

	master8192 := slots[8192]
	if master8192 == nil || master8192.Addr != "127.0.0.1:7001" {
		t.Errorf("slot 8192 should belong to 127.0.0.1:7001, got %v", master8192)
	}

	if master0.Role != RoleMaster {
		t.Error("node at slot 0 should be master")
	}
	if master8192.Role != RoleMaster {
		t.Error("node at slot 8192 should be master")
	}

	if len(master0.Replicas) != 1 || master0.Replicas[0].Addr != "127.0.0.1:7003" {
		t.Errorf("master0 should have replica 127.0.0.1:7003, got %v", master0.Replicas)
	}
	if len(master8192.Replicas) != 1 || master8192.Replicas[0].Addr != "127.0.0.1:7004" {
		t.Errorf("master8192 should have replica 127.0.0.1:7004, got %v", master8192.Replicas)
	}

	replica := nodes["127.0.0.1:7003"]
	if replica == nil {
		t.Fatal("replica node 127.0.0.1:7003 not found")
	}
	if replica.Role != RoleReplica {
		t.Error("127.0.0.1:7003 should be replica")
	}
	if replica.MasterID != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Errorf("replica MasterID wrong: %s", replica.MasterID)
	}
}

func TestParseClusterNodesSkipsFailedNodes(t *testing.T) {
	raw := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa 127.0.0.1:7000@17000 master - 0 1700000000000 1 connected 0-16383\n" +
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb 127.0.0.1:7001@17001 master,fail - 0 1700000000001 2 connected\n" +
		"cccccccccccccccccccccccccccccccccccccccc 127.0.0.1:7002@17002 master,pfail - 0 1700000000002 3 connected\n" +
		"dddddddddddddddddddddddddddddddddddddddd 127.0.0.1:7003@17003 master,noaddr - 0 1700000000003 4 connected\n" +
		"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee 127.0.0.1:7004@17004 master,handshake - 0 1700000000004 5 connected\n"

	nodes, _ := parseClusterNodes(raw)

	if _, ok := nodes["127.0.0.1:7000"]; !ok {
		t.Error("healthy master should be included")
	}
	if _, ok := nodes["127.0.0.1:7001"]; ok {
		t.Error("fail node should be excluded")
	}
	if _, ok := nodes["127.0.0.1:7002"]; ok {
		t.Error("pfail node should be excluded")
	}
	if _, ok := nodes["127.0.0.1:7003"]; ok {
		t.Error("noaddr node should be excluded")
	}
	if _, ok := nodes["127.0.0.1:7004"]; ok {
		t.Error("handshake node should be excluded")
	}
}

func TestParseClusterNodesSingleSlot(t *testing.T) {
	raw := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa 127.0.0.1:7000@17000 master - 0 1700000000000 1 connected 5000\n"

	nodes, slots := parseClusterNodes(raw)

	if len(nodes) != 1 {
		t.Errorf("expected 1 node, got %d", len(nodes))
	}
	if slots[5000] == nil || slots[5000].Addr != "127.0.0.1:7000" {
		t.Errorf("slot 5000 should belong to 127.0.0.1:7000")
	}
	if slots[4999] != nil {
		t.Error("slot 4999 should be nil")
	}
	if slots[5001] != nil {
		t.Error("slot 5001 should be nil")
	}
}

func TestParseClusterNodesAddrStripsClusterPort(t *testing.T) {
	raw := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa 127.0.0.1:7000@17000 master - 0 1700000000000 1 connected 0-16383\n"

	nodes, _ := parseClusterNodes(raw)

	if _, ok := nodes["127.0.0.1:7000"]; !ok {
		t.Error("node addr should strip @cport, expected 127.0.0.1:7000")
	}
	if _, ok := nodes["127.0.0.1:7000@17000"]; ok {
		t.Error("node addr should not include @cport")
	}
}

func TestParseClusterNodesMyselfFlag(t *testing.T) {
	raw := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa 127.0.0.1:7000@17000 myself,master - 0 1700000000000 1 connected 0-8191\n" +
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb 127.0.0.1:7001@17001 myself,slave aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa 0 1700000000001 1 connected\n"

	nodes, slots := parseClusterNodes(raw)

	if len(nodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(nodes))
	}
	master := nodes["127.0.0.1:7000"]
	if master == nil || master.Role != RoleMaster {
		t.Error("myself,master node should be RoleMaster")
	}
	replica := nodes["127.0.0.1:7001"]
	if replica == nil || replica.Role != RoleReplica {
		t.Error("myself,slave node should be RoleReplica")
	}
	if slots[0] == nil || slots[0].Addr != "127.0.0.1:7000" {
		t.Error("slot 0 should belong to myself,master node")
	}
}

func TestParseClusterNodesMultipleReplicas(t *testing.T) {
	raw := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa 127.0.0.1:7000@17000 master - 0 1700000000000 1 connected 0-16383\n" +
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb 127.0.0.1:7001@17001 slave aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa 0 1700000000001 1 connected\n" +
		"cccccccccccccccccccccccccccccccccccccccc 127.0.0.1:7002@17002 slave aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa 0 1700000000002 1 connected\n"

	nodes, _ := parseClusterNodes(raw)

	master := nodes["127.0.0.1:7000"]
	if master == nil {
		t.Fatal("master not found")
	}
	if len(master.Replicas) != 2 {
		t.Errorf("expected 2 replicas, got %d", len(master.Replicas))
	}
}

func TestParseClusterNodesEmptyInput(t *testing.T) {
	nodes, slots := parseClusterNodes("")
	if len(nodes) != 0 {
		t.Errorf("expected 0 nodes for empty input, got %d", len(nodes))
	}
	for i, n := range slots {
		if n != nil {
			t.Errorf("expected nil slot %d for empty input", i)
			break
		}
	}
}

func TestParseClusterNodesReplicaDetectedViaMasterID(t *testing.T) {
	raw := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa 127.0.0.1:7000@17000 master - 0 1700000000000 1 connected 0-16383\n" +
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb 127.0.0.1:7001@17001 slave aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa 0 1700000000001 1 connected\n"

	nodes, _ := parseClusterNodes(raw)

	replica := nodes["127.0.0.1:7001"]
	if replica == nil {
		t.Fatal("replica node not found")
	}
	if replica.Role != RoleReplica {
		t.Errorf("node with non-dash masterID should be RoleReplica, got %v", replica.Role)
	}
	if replica.MasterID != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Errorf("MasterID wrong: %s", replica.MasterID)
	}
}

func TestParseClusterNodesReplicaLinkedToMaster(t *testing.T) {
	raw := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa 127.0.0.1:7000@17000 master - 0 1700000000000 1 connected 0-5460\n" +
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb 127.0.0.1:7001@17001 master - 0 1700000000001 2 connected 5461-10922\n" +
		"cccccccccccccccccccccccccccccccccccccccc 127.0.0.1:7002@17002 master - 0 1700000000002 3 connected 10923-16383\n" +
		"dddddddddddddddddddddddddddddddddddddddd 127.0.0.1:7003@17003 slave aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa 0 1700000000003 1 connected\n" +
		"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee 127.0.0.1:7004@17004 slave bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb 0 1700000000004 2 connected\n" +
		"ffffffffffffffffffffffffffffffffffffffff 127.0.0.1:7005@17005 slave cccccccccccccccccccccccccccccccccccccccc 0 1700000000005 3 connected\n"

	nodes, slots := parseClusterNodes(raw)

	if len(nodes) != 6 {
		t.Errorf("expected 6 nodes, got %d", len(nodes))
	}

	m0 := nodes["127.0.0.1:7000"]
	m1 := nodes["127.0.0.1:7001"]
	m2 := nodes["127.0.0.1:7002"]
	if m0 == nil || m1 == nil || m2 == nil {
		t.Fatal("master nodes not found")
	}

	if len(m0.Replicas) != 1 || m0.Replicas[0].Addr != "127.0.0.1:7003" {
		t.Errorf("m0 replicas wrong: %v", m0.Replicas)
	}
	if len(m1.Replicas) != 1 || m1.Replicas[0].Addr != "127.0.0.1:7004" {
		t.Errorf("m1 replicas wrong: %v", m1.Replicas)
	}
	if len(m2.Replicas) != 1 || m2.Replicas[0].Addr != "127.0.0.1:7005" {
		t.Errorf("m2 replicas wrong: %v", m2.Replicas)
	}

	if slots[0] == nil || slots[0].Addr != "127.0.0.1:7000" {
		t.Errorf("slot 0 wrong: %v", slots[0])
	}
	if slots[5461] == nil || slots[5461].Addr != "127.0.0.1:7001" {
		t.Errorf("slot 5461 wrong: %v", slots[5461])
	}
	if slots[10923] == nil || slots[10923].Addr != "127.0.0.1:7002" {
		t.Errorf("slot 10923 wrong: %v", slots[10923])
	}
	if slots[16383] == nil || slots[16383].Addr != "127.0.0.1:7002" {
		t.Errorf("slot 16383 wrong: %v", slots[16383])
	}
}

func TestParseClusterNodesSlotBoundaries(t *testing.T) {
	raw := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa 127.0.0.1:7000@17000 master - 0 1700000000000 1 connected 0-16383\n"

	_, slots := parseClusterNodes(raw)

	if slots[0] == nil {
		t.Error("slot 0 (min) should be assigned")
	}
	if slots[16383] == nil {
		t.Error("slot 16383 (max) should be assigned")
	}
}

func TestParseClusterNodesLiveFormat(t *testing.T) {
	raw := "c9faea0939fe18d86e262074ed4dd287b2679304 10.102.181.14:18009 slave c1b4f1e3588170c50d102442584f3e3144b7ac04 0 1777630489384 4 connected\n" +
		"c1b4f1e3588170c50d102442584f3e3144b7ac04 10.102.181.14:18006 master - 0 1777630489785 3 connected 10923-16383\n" +
		"49e1a6f7214a354c990bc97420f4b3961205fb8c 10.102.181.14:18012 slave ee76f4471773a893f3b6e1446b30d1f6d60cb608 0 1777630489484 5 connected\n" +
		"e17d8c9d5b1e1a9a06e92c3cc1edf3bd9b8ce0fd 10.102.181.14:18003 master - 0 1777630489584 2 connected 5461-10922\n" +
		"ee76f4471773a893f3b6e1446b30d1f6d60cb608 10.102.181.14:18000 myself,master - 0 1777630488000 1 connected 0-5460\n" +
		"fadb07834a94e5bd7f2f6812f8d48ccf4a568d85 10.102.181.14:18015 slave e17d8c9d5b1e1a9a06e92c3cc1edf3bd9b8ce0fd 0 1777630489684 6 connected\n"

	nodes, slots := parseClusterNodes(raw)

	if len(nodes) != 6 {
		t.Errorf("expected 6 nodes, got %d", len(nodes))
	}

	masters := 0
	replicas := 0
	for _, n := range nodes {
		if n.Role == RoleMaster {
			masters++
		} else if n.Role == RoleReplica {
			replicas++
		}
	}
	if masters != 3 {
		t.Errorf("expected 3 masters, got %d", masters)
	}
	if replicas != 3 {
		t.Errorf("expected 3 replicas, got %d", replicas)
	}

	if slots[0] == nil || slots[0].Addr != "10.102.181.14:18000" {
		t.Errorf("slot 0 should be 10.102.181.14:18000, got %v", slots[0])
	}
	if slots[5460] == nil || slots[5460].Addr != "10.102.181.14:18000" {
		t.Errorf("slot 5460 should be 10.102.181.14:18000, got %v", slots[5460])
	}
	if slots[5461] == nil || slots[5461].Addr != "10.102.181.14:18003" {
		t.Errorf("slot 5461 should be 10.102.181.14:18003, got %v", slots[5461])
	}
	if slots[10923] == nil || slots[10923].Addr != "10.102.181.14:18006" {
		t.Errorf("slot 10923 should be 10.102.181.14:18006, got %v", slots[10923])
	}
	if slots[16383] == nil || slots[16383].Addr != "10.102.181.14:18006" {
		t.Errorf("slot 16383 should be 10.102.181.14:18006, got %v", slots[16383])
	}

	m0 := nodes["10.102.181.14:18000"]
	if m0 == nil || len(m0.Replicas) != 1 {
		t.Errorf("myself,master should have 1 replica, got %v", m0)
	}
}

func TestParseClusterNodesSkipsMigratingSlots(t *testing.T) {
	raw := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa 127.0.0.1:7000@17000 master - 0 1700000000000 1 connected 0-5460 [5461->-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb]\n" +
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb 127.0.0.1:7001@17001 master - 0 1700000000001 2 connected 5461-10922 [5461-<-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa]\n"

	nodes, slots := parseClusterNodes(raw)

	if len(nodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(nodes))
	}
	if slots[0] == nil || slots[0].Addr != "127.0.0.1:7000" {
		t.Errorf("slot 0 should belong to 127.0.0.1:7000")
	}
	if slots[5461] == nil || slots[5461].Addr != "127.0.0.1:7001" {
		t.Errorf("slot 5461 should belong to 127.0.0.1:7001")
	}
}

func TestOnTopologyChangedHookCalledForRemovedNodes(t *testing.T) {
	m := NewManager([]string{"127.0.0.1:7000"}, "", "")

	var mu sync.Mutex
	var gotRemoved []string
	m.SetNodeRemovedHook(func(addrs []string) {
		mu.Lock()
		gotRemoved = append(gotRemoved, addrs...)
		mu.Unlock()
	})

	prev := map[string]*Node{
		"127.0.0.1:7000": {ID: "n1", Addr: "127.0.0.1:7000", Role: RoleMaster},
		"127.0.0.1:7001": {ID: "n2", Addr: "127.0.0.1:7001", Role: RoleMaster},
		"127.0.0.1:7002": {ID: "n3", Addr: "127.0.0.1:7002", Role: RoleReplica},
	}
	next := map[string]*Node{
		"127.0.0.1:7000": {ID: "n1", Addr: "127.0.0.1:7000", Role: RoleMaster},
	}

	m.onTopologyChanged(prev, next)

	mu.Lock()
	removed := make([]string, len(gotRemoved))
	copy(removed, gotRemoved)
	mu.Unlock()

	sort.Strings(removed)
	if len(removed) != 2 {
		t.Fatalf("expected 2 removed addrs, got %v", removed)
	}
	if removed[0] != "127.0.0.1:7001" || removed[1] != "127.0.0.1:7002" {
		t.Errorf("unexpected removed addrs: %v", removed)
	}
}

func TestOnTopologyChangedHookNotCalledWhenNothingRemoved(t *testing.T) {
	m := NewManager([]string{"127.0.0.1:7000"}, "", "")

	called := false
	m.SetNodeRemovedHook(func(_ []string) { called = true })

	nodes := map[string]*Node{
		"127.0.0.1:7000": {ID: "n1", Addr: "127.0.0.1:7000", Role: RoleMaster},
	}
	m.onTopologyChanged(nodes, nodes)

	if called {
		t.Error("hook should not be called when no nodes are removed")
	}
}

func TestOnTopologyChangedUpdatesEntryPoints(t *testing.T) {
	m := NewManager([]string{"127.0.0.1:7000"}, "", "")

	next := map[string]*Node{
		"127.0.0.1:7001": {ID: "n2", Addr: "127.0.0.1:7001", Role: RoleMaster},
		"127.0.0.1:7002": {ID: "n3", Addr: "127.0.0.1:7002", Role: RoleMaster},
		"127.0.0.1:7003": {ID: "n4", Addr: "127.0.0.1:7003", Role: RoleReplica},
	}
	m.onTopologyChanged(map[string]*Node{}, next)

	eps := m.EntryPoints()
	sort.Strings(eps)
	if len(eps) != 3 {
		t.Fatalf("expected 3 entry points, got %v", eps)
	}
	if eps[0] != "127.0.0.1:7001" || eps[1] != "127.0.0.1:7002" || eps[2] != "127.0.0.1:7003" {
		t.Errorf("unexpected entry points: %v", eps)
	}
}

func TestOnTopologyChangedEmptyPrevDoesNotFireHook(t *testing.T) {
	m := NewManager([]string{"127.0.0.1:7000"}, "", "")

	called := false
	m.SetNodeRemovedHook(func(_ []string) { called = true })

	next := map[string]*Node{
		"127.0.0.1:7000": {ID: "n1", Addr: "127.0.0.1:7000", Role: RoleMaster},
	}
	m.onTopologyChanged(map[string]*Node{}, next)

	if called {
		t.Error("hook should not be called when prev is empty")
	}
}

func TestOnTopologyChangedAllNodesRemoved(t *testing.T) {
	m := NewManager([]string{"127.0.0.1:7000"}, "", "")

	var mu sync.Mutex
	var gotRemoved []string
	m.SetNodeRemovedHook(func(addrs []string) {
		mu.Lock()
		gotRemoved = append(gotRemoved, addrs...)
		mu.Unlock()
	})

	prev := map[string]*Node{
		"127.0.0.1:7000": {ID: "n1", Addr: "127.0.0.1:7000", Role: RoleMaster},
		"127.0.0.1:7001": {ID: "n2", Addr: "127.0.0.1:7001", Role: RoleMaster},
	}
	m.onTopologyChanged(prev, map[string]*Node{})

	mu.Lock()
	removed := make([]string, len(gotRemoved))
	copy(removed, gotRemoved)
	mu.Unlock()

	sort.Strings(removed)
	if len(removed) != 2 {
		t.Fatalf("expected 2 removed addrs, got %v", removed)
	}
	if removed[0] != "127.0.0.1:7000" || removed[1] != "127.0.0.1:7001" {
		t.Errorf("unexpected removed addrs: %v", removed)
	}

	eps := m.EntryPoints()
	if len(eps) != 0 {
		t.Errorf("entry points should be empty when all nodes removed, got %v", eps)
	}
}
