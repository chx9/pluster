package cluster

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pluster/pluster/pkg/proto"
)

const (
	ClusterSlots    = proto.ClusterSlots
	RefreshInterval = 5 * time.Second
	ConnectTimeout  = 3 * time.Second
)

type NodeRole int

const (
	RoleMaster NodeRole = iota
	RoleReplica
)

type Node struct {
	ID       string
	Addr     string
	Role     NodeRole
	Slots    [][2]int
	Replicas []*Node
	MasterID string
}

func (n *Node) String() string {
	return fmt.Sprintf("Node{id=%s, addr=%s, role=%d}", n.ID, n.Addr, n.Role)
}

// topoSnapshot holds an immutable snapshot of cluster topology.
// All fields are read-only after construction; no locking needed.
type topoSnapshot struct {
	nodes       map[string]*Node
	slots       [ClusterSlots]*Node
	masters     []*Node
	version     int64
	fingerprint string
}

// Topology is the public handle for cluster topology queries.
// It stores an atomic pointer to an immutable snapshot, so reads
// are lock-free. Writes swap the entire snapshot atomically.
type Topology struct {
	snap        atomic.Pointer[topoSnapshot]
	rrCounter   atomic.Uint64 // round-robin index for replica selection
	msrrCounter atomic.Uint64 // round-robin index for master-slave balanced reads
	version     atomic.Int64
}

func NewTopology() *Topology {
	t := &Topology{}
	t.snap.Store(&topoSnapshot{nodes: make(map[string]*Node)})
	return t
}

func (t *Topology) GetNodeForSlot(slot int) *Node {
	if slot < 0 || slot >= ClusterSlots {
		return nil
	}
	return t.snap.Load().slots[slot]
}

func (t *Topology) GetReplicaForSlot(slot int) *Node {
	if slot < 0 || slot >= ClusterSlots {
		return nil
	}
	snap := t.snap.Load()
	master := snap.slots[slot]
	if master == nil || len(master.Replicas) == 0 {
		return nil
	}
	idx := t.rrCounter.Add(1) % uint64(len(master.Replicas))
	return master.Replicas[idx]
}

func (t *Topology) GetNodeForSlotPreferReplica(slot int) *Node {
	if replica := t.GetReplicaForSlot(slot); replica != nil {
		return replica
	}
	return t.GetNodeForSlot(slot)
}

// GetNodeForSlotBalanced round-robins across master and all replicas for a slot.
func (t *Topology) GetNodeForSlotBalanced(slot int) *Node {
	if slot < 0 || slot >= ClusterSlots {
		return nil
	}
	snap := t.snap.Load()
	master := snap.slots[slot]
	if master == nil {
		return nil
	}
	all := make([]*Node, 0, 1+len(master.Replicas))
	all = append(all, master)
	all = append(all, master.Replicas...)
	idx := t.msrrCounter.Add(1) % uint64(len(all))
	return all[idx]
}

func (t *Topology) GetNodeByAddr(addr string) *Node {
	return t.snap.Load().nodes[addr]
}

func (t *Topology) AllMasters() []*Node {
	return t.snap.Load().masters
}

func (t *Topology) AllNodes() []*Node {
	nodes := t.snap.Load().nodes
	result := make([]*Node, 0, len(nodes))
	for _, n := range nodes {
		result = append(result, n)
	}
	return result
}

func (t *Topology) Version() int64 {
	return t.version.Load()
}

func (t *Topology) Update(nodes map[string]*Node, slots [ClusterSlots]*Node, fingerprint string) {
	masters := make([]*Node, 0, len(nodes))
	for _, n := range nodes {
		if n.Role == RoleMaster {
			masters = append(masters, n)
		}
	}
	sort.Slice(masters, func(i, j int) bool {
		return masters[i].Addr < masters[j].Addr
	})

	newVersion := t.version.Add(1)
	t.snap.Store(&topoSnapshot{
		nodes:       nodes,
		slots:       slots,
		masters:     masters,
		version:     newVersion,
		fingerprint: fingerprint,
	})
}

func topoFingerprint(nodes map[string]*Node) string {
	addrs := make([]string, 0, len(nodes))
	for addr, n := range nodes {
		role := "r"
		if n.Role == RoleMaster {
			role = "m"
		}
		slots := ""
		for _, r := range n.Slots {
			slots += fmt.Sprintf("%d-%d,", r[0], r[1])
		}
		addrs = append(addrs, addr+":"+role+":"+slots)
	}
	sort.Strings(addrs)
	return strings.Join(addrs, "|")
}

type NodeRemovedHook func(removedAddrs []string)

type Manager struct {
	entryPoints    []string
	password       string
	username       string
	topo           *Topology
	done           chan struct{}
	refreshCh      chan struct{}
	mu             sync.Mutex
	started        bool
	nodeRemovedHook NodeRemovedHook
}

func NewManager(entryPoints []string, username, password string) *Manager {
	return &Manager{
		entryPoints: entryPoints,
		password:    password,
		username:    username,
		topo:        NewTopology(),
		done:        make(chan struct{}),
		refreshCh:   make(chan struct{}, 1),
	}
}

func (m *Manager) SetNodeRemovedHook(hook NodeRemovedHook) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nodeRemovedHook = hook
}

func (m *Manager) EntryPoints() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]string, len(m.entryPoints))
	copy(cp, m.entryPoints)
	return cp
}

func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return nil
	}
	m.started = true
	m.mu.Unlock()

	if err := m.refresh(ctx); err != nil {
		return fmt.Errorf("initial topology load failed: %w", err)
	}

	go m.refreshLoop(ctx)
	return nil
}

func (m *Manager) Stop() {
	close(m.done)
}

func (m *Manager) LoadTopo() *Topology {
	return m.topo
}

func (m *Manager) TriggerRefresh() {
	select {
	case m.refreshCh <- struct{}{}:
	default:
	}
}

func (m *Manager) refreshLoop(ctx context.Context) {
	ticker := time.NewTicker(RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.refresh(ctx); err != nil {
				slog.Warn("topology refresh failed", "err", err)
			}
		case <-m.refreshCh:
			if err := m.refresh(ctx); err != nil {
				slog.Warn("topology refresh failed", "err", err)
			}
		}
	}
}

func (m *Manager) refresh(ctx context.Context) error {
	var errs []error
	for _, ep := range m.entryPoints {
		if err := m.refreshFrom(ctx, ep); err == nil {
			return nil
		} else {
			slog.Warn("topology refresh from entry point failed", "addr", ep, "err", err)
			errs = append(errs, fmt.Errorf("%s: %w", ep, err))
		}
	}
	return fmt.Errorf("topology refresh failed for all entry points: %w", errors.Join(errs...))
}

func (m *Manager) refreshFrom(ctx context.Context, addr string) error {
	conn, err := dialWithTimeout(addr, ConnectTimeout)
	if err != nil {
		return err
	}
	defer conn.Close()

	if m.password != "" {
		if err := authenticate(conn, m.username, m.password); err != nil {
			return err
		}
	}

	raw, err := fetchClusterNodes(conn)
	if err != nil {
		return err
	}

	nodes, slotMap := parseClusterNodes(raw)
	fingerprint := topoFingerprint(nodes)
	prevSnap := m.topo.snap.Load()
	m.topo.Update(nodes, slotMap, fingerprint)

	if fingerprint != prevSnap.fingerprint {
		logTopology(nodes, slotMap)
		m.onTopologyChanged(prevSnap.nodes, nodes)
	}
	return nil
}

func (m *Manager) onTopologyChanged(prev, next map[string]*Node) {
	var removed []string
	for addr := range prev {
		if _, ok := next[addr]; !ok {
			removed = append(removed, addr)
		}
	}
	if len(removed) > 0 {
		slog.Info("cluster nodes removed", "addrs", removed)
		m.mu.Lock()
		hook := m.nodeRemovedHook
		m.mu.Unlock()
		if hook != nil {
			hook(removed)
		}
	}

	newEntryPoints := make([]string, 0, len(next))
	for addr := range next {
		newEntryPoints = append(newEntryPoints, addr)
	}
	sort.Strings(newEntryPoints)
	m.mu.Lock()
	m.entryPoints = newEntryPoints
	m.mu.Unlock()
}

func logTopology(nodes map[string]*Node, slots [ClusterSlots]*Node) {
	masters := make([]*Node, 0, len(nodes))
	for _, n := range nodes {
		if n.Role == RoleMaster {
			masters = append(masters, n)
		}
	}
	sort.Slice(masters, func(i, j int) bool {
		return masters[i].Addr < masters[j].Addr
	})

	coveredSlots := 0
	for _, n := range slots {
		if n != nil {
			coveredSlots++
		}
	}

	slog.Info("cluster topology refreshed",
		"masters", len(masters),
		"total_nodes", len(nodes),
		"slots_covered", fmt.Sprintf("%d/16384", coveredSlots),
	)
	for _, m := range masters {
		slotRanges := make([]string, 0, len(m.Slots))
		for _, r := range m.Slots {
			if r[0] == r[1] {
				slotRanges = append(slotRanges, strconv.Itoa(r[0]))
			} else {
				slotRanges = append(slotRanges, fmt.Sprintf("%d-%d", r[0], r[1]))
			}
		}
		replicaAddrs := make([]string, 0, len(m.Replicas))
		for _, r := range m.Replicas {
			replicaAddrs = append(replicaAddrs, r.Addr)
		}
		slog.Info("  master node",
			"addr", m.Addr,
			"slots", strings.Join(slotRanges, ","),
			"replicas", strings.Join(replicaAddrs, ","),
		)
	}
}

func dialWithTimeout(addr string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("tcp", addr, timeout)
}

func authenticate(conn net.Conn, username, password string) error {
	w := proto.NewWriter(conn)
	r := proto.NewReader(conn)
	if username != "" {
		if err := w.WriteCommand("AUTH", username, password); err != nil {
			return err
		}
	} else {
		if err := w.WriteCommand("AUTH", password); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	v, err := r.ReadValue()
	if err != nil {
		return err
	}
	if v.IsError() {
		return errors.New("auth failed: " + v.Error())
	}
	return nil
}

func fetchClusterNodes(conn net.Conn) (string, error) {
	w := proto.NewWriter(conn)
	r := proto.NewReader(conn)
	if err := w.WriteCommand("CLUSTER", "NODES"); err != nil {
		return "", err
	}
	if err := w.Flush(); err != nil {
		return "", err
	}
	v, err := r.ReadValue()
	if err != nil {
		return "", err
	}
	if v.IsError() {
		return "", errors.New(v.Error())
	}
	return string(v.Str), nil
}

func parseClusterNodes(raw string) (map[string]*Node, [ClusterSlots]*Node) {
	nodes := make(map[string]*Node)
	var slots [ClusterSlots]*Node

	lines := strings.Split(strings.TrimSpace(raw), "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}

		nodeID := fields[0]
		addrField := fields[1]
		flagsField := fields[2]
		masterID := fields[3]

		addr := strings.SplitN(addrField, "@", 2)[0]
		if addr == "" || addr == ":0" {
			continue
		}

		flags := strings.Split(flagsField, ",")
		flagSet := make(map[string]bool, len(flags))
		for _, f := range flags {
			flagSet[f] = true
		}

		if flagSet["noaddr"] || flagSet["handshake"] {
			continue
		}
		if flagSet["fail"] || flagSet["pfail"] {
			continue
		}

		n := nodes[addr]
		if n == nil {
			n = &Node{
				ID:   nodeID,
				Addr: addr,
			}
			nodes[addr] = n
		} else {
			if n.ID == "" {
				n.ID = nodeID
			}
		}

		isMaster := flagSet["master"]
		isSlave := flagSet["slave"] || flagSet["replica"] || (masterID != "-" && masterID != "")

		if isMaster {
			n.Role = RoleMaster
			n.MasterID = ""
		} else if isSlave {
			n.Role = RoleReplica
			if masterID != "-" && masterID != "" {
				n.MasterID = masterID
			}
		}

		if isMaster {
			for _, slotField := range fields[8:] {
				if strings.HasPrefix(slotField, "[") {
					continue
				}
				if idx := strings.Index(slotField, "-"); idx >= 0 {
					start, err1 := strconv.Atoi(slotField[:idx])
					end, err2 := strconv.Atoi(slotField[idx+1:])
					if err1 != nil || err2 != nil {
						continue
					}
					n.Slots = append(n.Slots, [2]int{start, end})
					for s := start; s <= end; s++ {
						if s >= 0 && s < ClusterSlots {
							slots[s] = n
						}
					}
				} else {
					s, err := strconv.Atoi(slotField)
					if err != nil {
						continue
					}
					n.Slots = append(n.Slots, [2]int{s, s})
					if s >= 0 && s < ClusterSlots {
						slots[s] = n
					}
				}
			}
		}
	}

	for _, n := range nodes {
		if n.Role == RoleReplica && n.MasterID != "" {
			for _, m := range nodes {
				if m.ID == n.MasterID {
					m.Replicas = append(m.Replicas, n)
					break
				}
			}
		}
	}

	return nodes, slots
}


