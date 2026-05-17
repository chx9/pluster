package router

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pluster/pluster/pkg/cluster"
	"github.com/pluster/pluster/pkg/config"
	"github.com/pluster/pluster/pkg/pool"
	"github.com/pluster/pluster/pkg/proto"
)

const (
	MaxRedirects          = 16
	AskingCmdBytes        = "*1\r\n$6\r\nASKING\r\n"
	clusterDownMaxRetry   = 3
	clusterDownRetryDelay = 100 * time.Millisecond
	pipeRetryMax          = 2
	pipeRetryDelay        = 150 * time.Millisecond
)

type TopoRefresher interface {
	TriggerRefresh()
}

type Router struct {
	topo        *cluster.Topology
	poolMgr     *pool.Manager
	pipeMgr     *pool.PipelinedManager
	topoRefresh TopoRefresher
	mu          sync.RWMutex
	rrCounter   atomic.Uint64
	readMode    atomic.Int32
}

func New(topo *cluster.Topology, poolMgr *pool.Manager) *Router {
	r := &Router{
		topo:    topo,
		poolMgr: poolMgr,
		pipeMgr: pool.NewPipelinedManager(poolMgr.Username(), poolMgr.Password(), 16),
	}
	r.readMode.Store(int32(config.ReadModeMasterOnly))
	return r
}

type result struct {
	v   *proto.Value
	err error
}

type batchItem struct {
	idx  int
	req  *proto.Request
	addr string
	slot int
}

func (r *Router) SetTopoRefresher(tr TopoRefresher) {
	r.topoRefresh = tr
}

func (r *Router) SetReadMode(m config.ReadMode) {
	r.readMode.Store(int32(m))
}

func (r *Router) ReadMode() config.ReadMode {
	return config.ReadMode(r.readMode.Load())
}

func (r *Router) NodeAddrForSlot(slot int, isRead bool) (string, error) {
	return r.nodeAddrForSlot(slot, isRead)
}

func (r *Router) nodeAddrForSlot(slot int, isRead bool) (string, error) {
	rm := r.ReadMode()
	switch {
	case isRead && rm == config.ReadModeMasterSlave:
		node := r.topo.GetNodeForSlotBalanced(slot)
		if node == nil {
			return "", fmt.Errorf("no node for slot %d", slot)
		}
		return node.Addr, nil
	case isRead && rm == config.ReadModeSlaveOnly:
		node := r.topo.GetReplicaForSlot(slot)
		if node == nil {
			node = r.topo.GetNodeForSlot(slot)
		}
		if node == nil {
			return "", fmt.Errorf("no node for slot %d", slot)
		}
		return node.Addr, nil
	default:
		node := r.topo.GetNodeForSlot(slot)
		if node == nil {
			return "", fmt.Errorf("no node for slot %d", slot)
		}
		return node.Addr, nil
	}
}

func (r *Router) Execute(ctx context.Context, req *proto.Request) (*proto.Value, error) {
	if len(req.Args) == 0 {
		return nil, fmt.Errorf("empty request")
	}

	name := strings.ToUpper(string(req.Args[0]))
	info := proto.GetCmdInfo(name)

	if info == nil {
		return proto.ErrValue(fmt.Sprintf("ERR unknown command '%s', with args beginning with: ", strings.ToLower(name))), nil
	}

	if info.IsNotAllowed() {
		return proto.ErrValue(fmt.Sprintf("ERR command '%s' is not supported in cluster proxy", strings.ToLower(name))), nil
	}

	if name == "SCAN" {
		return r.executeSCAN(ctx, req)
	}

	// SCRIPT LOAD must be broadcast to all masters so that the SHA is
	// available on every node for subsequent EVALSHA calls.
	// SCRIPT FLUSH and SCRIPT EXISTS also benefit from all-nodes routing.
	if name == "SCRIPT" {
		return r.executeScript(ctx, req)
	}

	if name == "CONFIG" {
		return r.executeConfig(ctx, req)
	}

	if name == "BROADCAST" {
		return r.ExecuteBroadcast(ctx, req)
	}

	if info.IsAllNodes() {
		return r.executeAllNodes(ctx, req, name)
	}

	if info.IsMultiKey() {
		return r.executeMultiKey(ctx, req, info)
	}

	key := getFirstKey(req, info)
	if key == nil {
		return r.executeOnAnyNode(ctx, req)
	}

	slot := proto.HashSlot(key)
	return r.executeRedirect(ctx, req, "", slot, false, 0)
}

func (r *Router) executeRedirect(ctx context.Context, req *proto.Request, addr string, slot int, asking bool, depth int) (*proto.Value, error) {
	if depth > MaxRedirects {
		return nil, fmt.Errorf("too many redirects")
	}

	if addr == "" {
		info := proto.GetCmdInfo(req.Cmd)
		isRead := info != nil && info.IsRead() && !info.IsWrite()
		var err error
		addr, err = r.nodeAddrForSlot(slot, isRead)
		if err != nil {
			return nil, err
		}
	}

	v, err := r.execOnNode(ctx, addr, req, asking)
	if err != nil {
		return nil, err
	}

	if v.IsMovedError() {
		newSlot, newAddr, e := v.ParseRedirection()
		if e != nil {
			return v, nil
		}
		if r.topoRefresh != nil {
			r.topoRefresh.TriggerRefresh()
		}
		return r.executeRedirect(ctx, req, newAddr, newSlot, false, depth+1)
	}

	if v.IsAskError() {
		_, newAddr, e := v.ParseRedirection()
		if e != nil {
			return v, nil
		}
		return r.executeRedirect(ctx, req, newAddr, -1, true, depth+1)
	}

	return v, nil
}

func backoffDelay(attempt int, base time.Duration) time.Duration {
	return base * time.Duration(1<<uint(attempt))
}

func (r *Router) execOnNode(ctx context.Context, addr string, req *proto.Request, asking bool) (*proto.Value, error) {
	var v *proto.Value
	var lastErr error
	for attempt := 0; attempt <= clusterDownMaxRetry; attempt++ {
		var err error
		v, err = r.execOnNodeOnce(ctx, addr, req, asking)
		if err != nil {
			lastErr = err
			if attempt < pipeRetryMax {
				t := time.NewTimer(backoffDelay(attempt, pipeRetryDelay))
				select {
				case <-ctx.Done():
					t.Stop()
					return nil, ctx.Err()
				case <-t.C:
				}
				t.Stop()
				continue
			}
			return nil, lastErr
		}
		if !v.IsClusterDownError() && !v.IsLoadingError() {
			return v, nil
		}
		if attempt < clusterDownMaxRetry {
			t := time.NewTimer(backoffDelay(attempt, clusterDownRetryDelay))
			select {
			case <-ctx.Done():
				t.Stop()
				return nil, ctx.Err()
			case <-t.C:
			}
			t.Stop()
		}
	}
	return v, nil
}

func (r *Router) isReplicaAddr(addr string) bool {
	node := r.topo.GetNodeByAddr(addr)
	return node != nil && node.Role == cluster.RoleReplica
}

func (r *Router) execOnNodeOnce(ctx context.Context, addr string, req *proto.Request, asking bool) (*proto.Value, error) {
	if !asking {
		data := proto.EncodeCommandBytes(req.Args...)
		if r.isReplicaAddr(addr) {
			return r.pipeMgr.GetReadonlyPool(addr).Do(data)
		}
		return r.pipeMgr.GetPool(addr).Do(data)
	}

	p := r.poolMgr.GetPool(addr)
	conn, err := p.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("get conn from %s: %w", addr, err)
	}
	defer conn.Close()

	w := conn.Writer
	rd := conn.Reader

	if _, err := conn.Write([]byte(AskingCmdBytes)); err != nil {
		conn.MarkBroken()
		return nil, err
	}

	if err := w.WriteCommandBytes(req.Args...); err != nil {
		conn.MarkBroken()
		return nil, err
	}
	if err := w.Flush(); err != nil {
		conn.MarkBroken()
		return nil, err
	}

	askReply, err := rd.ReadValue()
	if err != nil {
		conn.MarkBroken()
		return nil, err
	}
	if askReply.IsError() {
		return askReply, nil
	}

	v, err := rd.ReadValue()
	if err != nil {
		conn.MarkBroken()
		return nil, err
	}
	return v, nil
}

func (r *Router) executeOnAnyNode(ctx context.Context, req *proto.Request) (*proto.Value, error) {
	masters := r.topo.AllMasters()
	if len(masters) == 0 {
		return nil, fmt.Errorf("no cluster nodes available")
	}
	idx := r.rrCounter.Add(1) % uint64(len(masters))
	return r.execOnNode(ctx, masters[idx].Addr, req, false)
}

func (r *Router) fanOutToMasters(ctx context.Context, fn func(addr string) result) ([]result, error) {
	masters := r.topo.AllMasters()
	if len(masters) == 0 {
		return nil, fmt.Errorf("no cluster nodes available")
	}
	results := make([]result, len(masters))
	var wg sync.WaitGroup
	for i, node := range masters {
		wg.Add(1)
		go func(idx int, addr string) {
			defer wg.Done()
			results[idx] = fn(addr)
		}(i, node.Addr)
	}
	wg.Wait()
	return results, nil
}

func (r *Router) executeAllNodes(ctx context.Context, req *proto.Request, name string) (*proto.Value, error) {
	results, err := r.fanOutToMasters(ctx, func(addr string) result {
		v, e := r.execOnNode(ctx, addr, req, false)
		return result{v, e}
	})
	if err != nil {
		return nil, err
	}

	switch name {
	case "DBSIZE":
		return mergeIntegerSum(results)
	case "KEYS":
		return mergeArrayConcat(results)
	case "FLUSHDB", "FLUSHALL":
		return mergeOK(results)
	case "SCRIPT":
		return mergeFirstNonError(results)
	default:
		return mergeArrayConcat(results)
	}
}

const (
	scanNodeIndexBits = 16
	scanNodeIndexMask = (1 << scanNodeIndexBits) - 1
)

func (r *Router) executeSCAN(ctx context.Context, req *proto.Request) (*proto.Value, error) {
	if len(req.Args) < 2 {
		return proto.ErrValue("ERR wrong number of arguments for 'scan' command"), nil
	}

	masters := r.topo.AllMasters()
	if len(masters) == 0 {
		return nil, fmt.Errorf("no cluster nodes available")
	}

	cursorStr := string(req.Args[1])
	clientCursor, err := strconv.ParseUint(cursorStr, 10, 64)
	if err != nil {
		return proto.ErrValue("ERR value is not an integer or out of range"), nil
	}

	nodeIdx := int(clientCursor & scanNodeIndexMask)
	realCursor := clientCursor >> scanNodeIndexBits

	if nodeIdx >= len(masters) {
		nodeIdx = 0
		realCursor = 0
	}

	scanArgs := make([][]byte, len(req.Args))
	copy(scanArgs, req.Args)
	scanArgs[1] = []byte(strconv.FormatUint(realCursor, 10))
	scanReq := &proto.Request{Args: scanArgs}

	addr := masters[nodeIdx].Addr
	v, err := r.execOnNode(ctx, addr, scanReq, false)
	if err != nil {
		return nil, err
	}
	if v.IsError() {
		return v, nil
	}

	if v.Type != proto.TypeArray || len(v.Array) < 2 {
		return v, nil
	}

	returnedCursorStr := string(v.Array[0].Str)
	returnedCursor, err := strconv.ParseUint(returnedCursorStr, 10, 64)
	if err != nil {
		return v, nil
	}

	var nextClientCursor uint64
	if returnedCursor != 0 {
		nextClientCursor = (returnedCursor << scanNodeIndexBits) | uint64(nodeIdx)
	} else {
		nextIdx := nodeIdx + 1
		if nextIdx < len(masters) {
			nextClientCursor = uint64(nextIdx)
		}
	}

	newCursorBytes := []byte(strconv.FormatUint(nextClientCursor, 10))
	newReply := &proto.Value{
		Type: proto.TypeArray,
		Array: []*proto.Value{
			{Type: proto.TypeBulkString, Str: newCursorBytes},
			v.Array[1],
		},
	}
	return newReply, nil
}

func (r *Router) executeMultiKey(ctx context.Context, req *proto.Request, info *proto.CmdInfo) (*proto.Value, error) {
	name := strings.ToUpper(string(req.Args[0]))

	switch name {
	case "MGET":
		return r.executeMGET(ctx, req)
	case "MSET", "MSETNX":
		return r.executeMSET(ctx, req, name)
	case "DEL", "UNLINK":
		return r.executeDEL(ctx, req, name)
	case "EXISTS", "TOUCH":
		return r.executeEXISTS(ctx, req)
	default:
		keys := proto.GetKeys(req)
		if len(keys) == 0 {
			return r.executeOnAnyNode(ctx, req)
		}
		slot := proto.HashSlot(keys[0])
		return r.executeRedirect(ctx, req, "", slot, false, 0)
	}
}

func (r *Router) executeMGET(ctx context.Context, req *proto.Request) (*proto.Value, error) {
	keys := req.Args[1:]
	if len(keys) == 0 {
		return proto.ErrValue("ERR wrong number of arguments for 'mget' command"), nil
	}

	slotKeys := make(map[int][]int)
	for i, k := range keys {
		slot := proto.HashSlot(k)
		slotKeys[slot] = append(slotKeys[slot], i)
	}

	results := make([]*proto.Value, len(keys))
	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error

	for slot, indices := range slotKeys {
		wg.Add(1)
		go func(s int, idxs []int) {
			defer wg.Done()
			subKeys := make([][]byte, len(idxs)+1)
			subKeys[0] = []byte("MGET")
			for i, idx := range idxs {
				subKeys[i+1] = keys[idx]
			}
			subReq := &proto.Request{Args: subKeys}
			v, err := r.executeRedirect(ctx, subReq, "", s, false, 0)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			if v.IsError() {
				if firstErr == nil {
					firstErr = errors.New(v.Error())
				}
				return
			}
			if v.Type == proto.TypeArray {
				for i, idx := range idxs {
					if i < len(v.Array) {
						results[idx] = v.Array[i]
					}
				}
			}
		}(slot, indices)
	}
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	return &proto.Value{Type: proto.TypeArray, Array: results}, nil
}

func (r *Router) executeMSET(ctx context.Context, req *proto.Request, name string) (*proto.Value, error) {
	if len(req.Args) < 3 || (len(req.Args)-1)%2 != 0 {
		return proto.ErrValue(fmt.Sprintf("ERR wrong number of arguments for '%s' command", strings.ToLower(name))), nil
	}

	slotPairs := make(map[int][][2]int)
	for i := 1; i < len(req.Args); i += 2 {
		slot := proto.HashSlot(req.Args[i])
		slotPairs[slot] = append(slotPairs[slot], [2]int{i, i + 1})
	}

	if name == "MSETNX" && len(slotPairs) > 1 {
		return proto.ErrValue("CROSSSLOT MSETNX requires all keys to be in the same slot (atomicity cannot be guaranteed across slots)"), nil
	}

	isMSETNX := name == "MSETNX"

	type slotResult struct {
		val *proto.Value
		err error
	}
	results := make([]slotResult, 0, len(slotPairs))
	resultCh := make(chan slotResult, len(slotPairs))

	var wg sync.WaitGroup

	for slot, pairs := range slotPairs {
		wg.Add(1)
		go func(s int, ps [][2]int) {
			defer wg.Done()
			args := make([][]byte, 1+len(ps)*2)
			args[0] = []byte(name)
			for i, p := range ps {
				args[1+i*2] = req.Args[p[0]]
				args[2+i*2] = req.Args[p[1]]
			}
			subReq := &proto.Request{Args: args}
			v, err := r.executeRedirect(ctx, subReq, "", s, false, 0)
			resultCh <- slotResult{val: v, err: err}
		}(slot, pairs)
	}
	wg.Wait()
	close(resultCh)

	var firstErr error
	for sr := range resultCh {
		results = append(results, sr)
		if sr.err != nil && firstErr == nil {
			firstErr = sr.err
		}
		if sr.val != nil && sr.val.IsError() && firstErr == nil {
			firstErr = errors.New(sr.val.Error())
		}
	}

	if firstErr != nil {
		return nil, firstErr
	}

	if isMSETNX {
		for _, res := range results {
			if res.val != nil && res.val.Type == proto.TypeInteger && res.val.Integer == 0 {
				return &proto.Value{Type: proto.TypeInteger, Integer: 0}, nil
			}
		}
		return &proto.Value{Type: proto.TypeInteger, Integer: 1}, nil
	}
	return &proto.Value{Type: proto.TypeSimpleString, Str: []byte("OK")}, nil
}

func (r *Router) executeDEL(ctx context.Context, req *proto.Request, name string) (*proto.Value, error) {
	keys := req.Args[1:]
	if len(keys) == 0 {
		return proto.ErrValue(fmt.Sprintf("ERR wrong number of arguments for '%s' command", strings.ToLower(name))), nil
	}
	return r.fanOutInteger(ctx, []byte(name), keys)
}

func (r *Router) executeEXISTS(ctx context.Context, req *proto.Request) (*proto.Value, error) {
	return r.fanOutInteger(ctx, req.Args[0], req.Args[1:])
}

func (r *Router) fanOutInteger(ctx context.Context, cmdName []byte, keys [][]byte) (*proto.Value, error) {
	slotKeys := make(map[int][][]byte)
	for _, k := range keys {
		slot := proto.HashSlot(k)
		slotKeys[slot] = append(slotKeys[slot], k)
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	var total int64
	var firstErr error

	for slot, ks := range slotKeys {
		wg.Add(1)
		go func(s int, skeys [][]byte) {
			defer wg.Done()
			args := make([][]byte, 1+len(skeys))
			args[0] = cmdName
			copy(args[1:], skeys)
			subReq := &proto.Request{Args: args}
			v, err := r.executeRedirect(ctx, subReq, "", s, false, 0)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			if v.Type == proto.TypeInteger {
				total += v.Integer
			}
		}(slot, ks)
	}
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}
	return &proto.Value{Type: proto.TypeInteger, Integer: total}, nil
}

func (r *Router) ExecOnNode(ctx context.Context, addr string, req *proto.Request) (*proto.Value, error) {
	return r.execOnNode(ctx, addr, req, false)
}

func (r *Router) Close() {
	if r.pipeMgr != nil {
		r.pipeMgr.Close()
	}
}

func (r *Router) ExecScript(ctx context.Context, req *proto.Request) (*proto.Value, error) {
	if len(req.Args) < 3 {
		return proto.ErrValue("ERR wrong number of arguments for EVAL"), nil
	}

	numKeysStr := string(req.Args[2])
	numKeys, err := strconv.Atoi(numKeysStr)
	if err != nil || numKeys < 0 {
		return proto.ErrValue("ERR value is not an integer or out of range"), nil
	}

	if numKeys == 0 {
		return r.executeOnAnyNode(ctx, req)
	}

	if len(req.Args) < 3+numKeys {
		return proto.ErrValue("ERR wrong number of arguments for EVAL"), nil
	}

	firstKey := req.Args[3]
	slot := proto.HashSlot(firstKey)

	if numKeys > 1 {
		for i := 4; i < 3+numKeys; i++ {
			if proto.HashSlot(req.Args[i]) != slot {
				return proto.ErrValue("CROSSSLOT Keys in script don't hash to the same slot"), nil
			}
		}
	}

	return r.executeRedirect(ctx, req, "", slot, false, 0)
}

func (r *Router) executeScript(ctx context.Context, req *proto.Request) (*proto.Value, error) {
	if len(req.Args) < 2 {
		return proto.ErrValue("ERR wrong number of arguments for 'script' command"), nil
	}
	sub := strings.ToUpper(string(req.Args[1]))

	switch sub {
	case "LOAD":
		return r.executeAllNodes(ctx, req, "SCRIPT")
	case "FLUSH":
		return r.executeAllNodes(ctx, req, "SCRIPT")
	case "EXISTS":
		return r.executeScriptExists(ctx, req)
	default:
		return r.executeOnAnyNode(ctx, req)
	}
}

func (r *Router) executeScriptExists(ctx context.Context, req *proto.Request) (*proto.Value, error) {
	results, err := r.fanOutToMasters(ctx, func(addr string) result {
		v, e := r.execOnNode(ctx, addr, req, false)
		return result{v, e}
	})
	if err != nil {
		return nil, err
	}

	for _, res := range results {
		if res.err != nil {
			return nil, res.err
		}
	}

	if len(results) == 0 {
		return &proto.Value{Type: proto.TypeArray, Array: nil}, nil
	}

	numSHAs := len(req.Args) - 2
	if numSHAs <= 0 {
		return &proto.Value{Type: proto.TypeArray, Array: nil}, nil
	}

	merged := make([]*proto.Value, numSHAs)
	for i := 0; i < numSHAs; i++ {
		allExist := true
		for _, res := range results {
			if res.v == nil || res.v.Type != proto.TypeArray || i >= len(res.v.Array) {
				allExist = false
				break
			}
			if res.v.Array[i].Type != proto.TypeInteger || res.v.Array[i].Integer == 0 {
				allExist = false
				break
			}
		}
		val := int64(0)
		if allExist {
			val = 1
		}
		merged[i] = &proto.Value{Type: proto.TypeInteger, Integer: val}
	}
	return &proto.Value{Type: proto.TypeArray, Array: merged}, nil
}

func (r *Router) Topo() *cluster.Topology {
	return r.topo
}

func (r *Router) PoolMgr() *pool.Manager {
	return r.poolMgr
}

func (r *Router) PipeMgr() *pool.PipelinedManager {
	return r.pipeMgr
}

func (r *Router) ExecPipeline(ctx context.Context, reqs []*proto.Request) ([]*proto.Value, error) {
	if len(reqs) == 0 {
		return nil, nil
	}
	results := make([]*proto.Value, len(reqs))
	complexIdxs, simple := r.classifyPipelineRequests(ctx, reqs, results)
	r.dispatchComplexRequests(ctx, reqs, complexIdxs, results)
	r.dispatchSimpleRequests(ctx, simple, results)
	return results, nil
}

func (r *Router) classifyPipelineRequests(ctx context.Context, reqs []*proto.Request, results []*proto.Value) ([]int, []batchItem) {
	var complexIdxs []int
	var simple []batchItem
	for i, req := range reqs {
		if len(req.Args) == 0 {
			results[i] = proto.ErrValue("ERR empty command")
			continue
		}
		name := strings.ToUpper(string(req.Args[0]))
		info := proto.GetCmdInfo(name)
		if info == nil {
			results[i] = proto.ErrValue(fmt.Sprintf("ERR unknown command '%s', with args beginning with: ", strings.ToLower(name)))
			continue
		}
		if info.IsNotAllowed() {
			results[i] = proto.ErrValue(fmt.Sprintf("ERR command '%s' is not supported in cluster proxy", strings.ToLower(name)))
			continue
		}
		if name == "SCAN" || name == "SCRIPT" || name == "CONFIG" || info.IsAllNodes() || info.IsMultiKey() {
			complexIdxs = append(complexIdxs, i)
			continue
		}
		key := getFirstKey(req, info)
		isRead := info.IsRead() && !info.IsWrite()
		var addr string
		var slot int
		if key == nil {
			masters := r.topo.AllMasters()
			if len(masters) == 0 {
				results[i] = proto.ErrValue("ERR no cluster nodes available")
				continue
			}
			idx := r.rrCounter.Add(1) % uint64(len(masters))
			addr = masters[idx].Addr
			slot = -1
		} else {
			slot = proto.HashSlot(key)
			var err error
			addr, err = r.nodeAddrForSlot(slot, isRead)
			if err != nil {
				results[i] = proto.ErrValue(fmt.Sprintf("ERR no node for slot %d", slot))
				continue
			}
		}
		simple = append(simple, batchItem{idx: i, req: req, addr: addr, slot: slot})
	}
	return complexIdxs, simple
}

func (r *Router) dispatchComplexRequests(ctx context.Context, reqs []*proto.Request, complexIdxs []int, results []*proto.Value) {
	for _, i := range complexIdxs {
		v, err := r.Execute(ctx, reqs[i])
		if err != nil {
			results[i] = proto.ErrValue(err.Error())
		} else {
			results[i] = v
		}
	}
}

func (r *Router) dispatchSimpleRequests(ctx context.Context, simple []batchItem, results []*proto.Value) {
	if len(simple) == 0 {
		return
	}
	byAddr := make(map[string][]batchItem, len(simple))
	for _, item := range simple {
		byAddr[item.addr] = append(byAddr[item.addr], item)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	for addr, items := range byAddr {
		wg.Add(1)
		go func(nodeAddr string, batch []batchItem) {
			defer wg.Done()
			vals := r.execBatchOnNode(ctx, nodeAddr, batch)
			mu.Lock()
			for j, item := range batch {
				results[item.idx] = vals[j]
			}
			mu.Unlock()
		}(addr, items)
	}
	wg.Wait()
}

func (r *Router) execBatchOnNode(ctx context.Context, addr string, batch []batchItem) []*proto.Value {
	results := make([]*proto.Value, len(batch))

	var p *pool.Pool
	if r.isReplicaAddr(addr) {
		p = r.poolMgr.GetReadonlyPool(addr)
	} else {
		p = r.poolMgr.GetPool(addr)
	}
	conn, err := p.Get(ctx)
	if err != nil {
		for i := range results {
			results[i] = proto.ErrValue(fmt.Sprintf("ERR get conn from %s: %v", addr, err))
		}
		return results
	}

	w := conn.Writer
	rd := conn.Reader

	for _, item := range batch {
		if err := w.WriteCommandBytes(item.req.Args...); err != nil {
			conn.MarkBroken()
			conn.Close()
			for i := range results {
				results[i] = proto.ErrValue(fmt.Sprintf("ERR write: %v", err))
			}
			return results
		}
	}
	if err := w.Flush(); err != nil {
		conn.MarkBroken()
		conn.Close()
		for i := range results {
			results[i] = proto.ErrValue(fmt.Sprintf("ERR flush: %v", err))
		}
		return results
	}

	needRedirect := false
	needClusterRetry := false
	for i := range batch {
		v, err := rd.ReadValue()
		if err != nil {
			conn.MarkBroken()
			conn.Close()
			for j := i; j < len(results); j++ {
				results[j] = proto.ErrValue(fmt.Sprintf("ERR read: %v", err))
			}
			return results
		}
		results[i] = v
		if v.IsMovedError() || v.IsAskError() {
			needRedirect = true
		}
		if v.IsClusterDownError() || v.IsLoadingError() {
			needClusterRetry = true
		}
	}
	conn.Close()

	if needClusterRetry {
		for i, item := range batch {
			v := results[i]
			if v.IsClusterDownError() || v.IsLoadingError() {
				rv, rerr := r.execOnNode(ctx, item.addr, item.req, false)
				if rerr != nil {
					results[i] = proto.ErrValue(rerr.Error())
				} else {
					results[i] = rv
				}
			}
		}
	}

	if needRedirect {
		for i, item := range batch {
			v := results[i]
			if v.IsMovedError() {
				newSlot, newAddr, e := v.ParseRedirection()
				if e == nil {
					if r.topoRefresh != nil {
						r.topoRefresh.TriggerRefresh()
					}
					rv, rerr := r.executeRedirect(ctx, item.req, newAddr, newSlot, false, 1)
					if rerr != nil {
						results[i] = proto.ErrValue(rerr.Error())
					} else {
						results[i] = rv
					}
				}
			} else if v.IsAskError() {
				_, newAddr, e := v.ParseRedirection()
				if e == nil {
					rv, rerr := r.executeRedirect(ctx, item.req, newAddr, -1, true, 1)
					if rerr != nil {
						results[i] = proto.ErrValue(rerr.Error())
					} else {
						results[i] = rv
					}
				}
			}
		}
	}

	return results
}

func getFirstKey(req *proto.Request, info *proto.CmdInfo) []byte {
	if info.KeySpec.FirstKey <= 0 || info.KeySpec.FirstKey >= len(req.Args) {
		return nil
	}
	return req.Args[info.KeySpec.FirstKey]
}


func mergeIntegerSum(results []result) (*proto.Value, error) {
	var total int64
	for _, r := range results {
		if r.err != nil {
			return nil, r.err
		}
		if r.v != nil && r.v.Type == proto.TypeInteger {
			total += r.v.Integer
		}
	}
	return &proto.Value{Type: proto.TypeInteger, Integer: total}, nil
}

func mergeArrayConcat(results []result) (*proto.Value, error) {
	var all []*proto.Value
	for _, r := range results {
		if r.err != nil {
			return nil, r.err
		}
		if r.v == nil {
			continue
		}
		if r.v.Type == proto.TypeArray {
			all = append(all, r.v.Array...)
		}
	}
	return &proto.Value{Type: proto.TypeArray, Array: all}, nil
}

func mergeOK(results []result) (*proto.Value, error) {
	for _, r := range results {
		if r.err != nil {
			return nil, r.err
		}
		if r.v != nil && r.v.IsError() {
			return r.v, nil
		}
	}
	return &proto.Value{Type: proto.TypeSimpleString, Str: []byte("OK")}, nil
}

func mergeFirstNonError(results []result) (*proto.Value, error) {
	var firstErr error
	for _, r := range results {
		if r.err != nil {
			if firstErr == nil {
				firstErr = r.err
			}
			continue
		}
		if r.v != nil && !r.v.IsError() {
			return r.v, nil
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	if len(results) > 0 && results[0].v != nil {
		return results[0].v, nil
	}
	return &proto.Value{Type: proto.TypeSimpleString, Str: []byte("OK")}, nil
}

func (r *Router) ExecuteBroadcast(ctx context.Context, req *proto.Request) (*proto.Value, error) {
	if len(req.Args) < 2 {
		return proto.ErrValue("ERR wrong number of arguments for 'broadcast' command"), nil
	}

	subReq := &proto.Request{Args: req.Args[1:]}

	masters := r.topo.AllMasters()
	if len(masters) == 0 {
		return nil, fmt.Errorf("no cluster nodes available")
	}

	type nodeResult struct {
		addr string
		v    *proto.Value
		err  error
	}

	results := make([]nodeResult, len(masters))
	var wg sync.WaitGroup
	for i, node := range masters {
		wg.Add(1)
		go func(idx int, addr string) {
			defer wg.Done()
			v, err := r.execOnNode(ctx, addr, subReq, false)
			results[idx] = nodeResult{addr: addr, v: v, err: err}
		}(i, node.Addr)
	}
	wg.Wait()

	arr := make([]*proto.Value, len(results))
	for i, res := range results {
		addrVal := &proto.Value{Type: proto.TypeBulkString, Str: []byte(res.addr)}
		var replyVal *proto.Value
		if res.err != nil {
			replyVal = &proto.Value{Type: proto.TypeError, Str: []byte("ERR " + res.err.Error())}
		} else {
			replyVal = res.v
		}
		arr[i] = &proto.Value{
			Type:  proto.TypeArray,
			Array: []*proto.Value{addrVal, replyVal},
		}
	}
	return &proto.Value{Type: proto.TypeArray, Array: arr}, nil
}

func (r *Router) executeConfig(ctx context.Context, req *proto.Request) (*proto.Value, error) {
	if len(req.Args) < 2 {
		return proto.ErrValue("ERR wrong number of arguments for 'config' command"), nil
	}
	sub := strings.ToUpper(string(req.Args[1]))
	switch sub {
	case "GET", "SET", "RESETSTAT":
		results, err := r.fanOutToMasters(ctx, func(addr string) result {
			v, e := r.execOnNode(ctx, addr, req, false)
			return result{v, e}
		})
		if err != nil {
			return nil, err
		}
		switch sub {
		case "GET":
			return mergeConfigGet(results)
		case "SET", "RESETSTAT":
			return mergeOK(results)
		}
	}
	return r.executeOnAnyNode(ctx, req)
}

func mergeConfigGet(results []result) (*proto.Value, error) {
	seen := make(map[string]bool)
	var merged []*proto.Value
	for _, res := range results {
		if res.err != nil {
			return nil, res.err
		}
		if res.v == nil {
			continue
		}
		if res.v.IsError() {
			return res.v, nil
		}
		if res.v.Type != proto.TypeArray {
			continue
		}
		for i := 0; i+1 < len(res.v.Array); i += 2 {
			key := string(res.v.Array[i].Str)
			if !seen[key] {
				seen[key] = true
				merged = append(merged, res.v.Array[i], res.v.Array[i+1])
			}
		}
	}
	return &proto.Value{Type: proto.TypeArray, Array: merged}, nil
}
