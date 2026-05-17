package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	gnet "github.com/panjf2000/gnet/v2"

	"github.com/pluster/pluster/pkg/proto"
)

func (h *ProxyHandler) dispatchClientRequest(c gnet.Conn, cctx *clientCtx, req *proto.Request) gnet.Action {
	if len(req.Args) == 0 {
		_, _ = c.Write(proto.EncodeError("ERR empty command"))
		return gnet.None
	}

	cctx.mux.localCmdCount++
	name := req.Cmd

	switch cctx.state {
	case stateMulti, stateWatchMulti:
		return h.handleInMulti(c, cctx, req, name)
	case stateWatch:
		return h.handleNormal(c, cctx, req, name)
	case stateSubscribe:
		return h.handleInSubscribe(c, cctx, req, name)
	case stateBlocking:
		if name == "QUIT" || name == "RESET" {
			return h.handleNormal(c, cctx, req, name)
		}
		cctx.pendingCmds = append(cctx.pendingCmds, req)
		return gnet.None
	}

	return h.handleNormal(c, cctx, req, name)
}

func (h *ProxyHandler) handleNormal(c gnet.Conn, cctx *clientCtx, req *proto.Request, name string) gnet.Action {
	if !cctx.authenticated && name != "AUTH" && name != "QUIT" && name != "HELLO" && name != "PING" {
		h.writeImmediate(c, cctx, proto.EncodeError("NOAUTH Authentication required."))
		return gnet.None
	}

	// WATCH is dialing a dedicated backend connection. Buffer subsequent commands
	// until the WATCH response arrives (same as stateBlocking), so that MULTI/EXEC
	// issued in the same pipeline see the correct stateWatch state.
	if cctx.pendingWatchReq != nil && name != "QUIT" && name != "RESET" {
		cctx.pendingCmds = append(cctx.pendingCmds, req)
		return gnet.None
	}

	switch name {
	case "QUIT":
		_, _ = c.Write(proto.EncodeSimpleString("OK"))
		return gnet.Close

	case "RESET":
		h.resetClientState(c, cctx)
		h.writeImmediate(c, cctx, proto.EncodeSimpleString("RESET"))
		return gnet.None

	case "PING":
		var data []byte
		if len(req.Args) == 1 {
			data = proto.EncodeSimpleString("PONG")
		} else {
			data = proto.EncodeBulkString(req.Args[1])
		}
		h.writeImmediate(c, cctx, data)
		return gnet.None

	case "ECHO":
		if len(req.Args) != 2 {
			h.writeImmediate(c, cctx, proto.EncodeError("ERR wrong number of arguments for 'echo' command"))
			return gnet.None
		}
		h.writeImmediate(c, cctx, proto.EncodeBulkString(req.Args[1]))
		return gnet.None

	case "SELECT":
		if len(req.Args) != 2 {
			h.writeImmediate(c, cctx, proto.EncodeError("ERR wrong number of arguments for 'select' command"))
			return gnet.None
		}
		if string(req.Args[1]) != "0" {
			h.writeImmediate(c, cctx, proto.EncodeError("ERR SELECT is not allowed in cluster mode"))
			return gnet.None
		}
		h.writeImmediate(c, cctx, proto.EncodeSimpleString("OK"))
		return gnet.None

	case "READONLY":
		if len(req.Args) != 1 {
			h.writeImmediate(c, cctx, proto.EncodeError("ERR wrong number of arguments for 'readonly' command"))
			return gnet.None
		}
		h.writeImmediate(c, cctx, proto.EncodeSimpleString("OK"))
		return gnet.None

	case "READWRITE":
		if len(req.Args) != 1 {
			h.writeImmediate(c, cctx, proto.EncodeError("ERR wrong number of arguments for 'readwrite' command"))
			return gnet.None
		}
		h.writeImmediate(c, cctx, proto.EncodeSimpleString("OK"))
		return gnet.None

	case "HELLO":
		return h.handleHELLO(c, cctx, req)

	case "AUTH":
		return h.handleAUTH(c, cctx, req)

	case "CLIENT":
		return h.handleClientCmd(c, cctx, req)

	case "INFO":
		return h.handleViaRouter(c, cctx, req)

	case "CLUSTER":
		return h.handleCLUSTER(c, cctx, req)

	case "MULTI":
		if cctx.state == stateWatch {
			cctx.state = stateWatchMulti
		} else {
			cctx.state = stateMulti
		}
		cctx.txQueue = cctx.txQueue[:0]
		cctx.txErr = false
		h.writeImmediate(c, cctx, proto.EncodeSimpleString("OK"))
		return gnet.None

	case "WATCH":
		return h.handleWATCH(c, cctx, req)

	case "UNWATCH":
		return h.handleUNWATCH(c, cctx)

	case "DISCARD":
		h.writeImmediate(c, cctx, proto.EncodeError("ERR DISCARD without MULTI"))
		return gnet.None

	case "EXEC":
		h.writeImmediate(c, cctx, proto.EncodeError("ERR EXEC without MULTI"))
		return gnet.None

	case "SUBSCRIBE", "PSUBSCRIBE":
		return h.startSubscribe(c, cctx, req)

	case "EVAL", "EVALSHA", "EVAL_RO", "EVALSHA_RO":
		return h.handleViaRouterScript(c, cctx, req)

	case "PROXY":
		return h.handleProxyCmd(c, cctx, req)

	case "BROADCAST":
		return h.submitToRouter(c, cctx, func(ctx context.Context) (*proto.Value, error) {
			return cctx.router.ExecuteBroadcast(ctx, req)
		})

	default:
		info := proto.GetCmdInfo(req.Cmd)
		if info != nil && info.IsBlocking() {
			return h.dispatchBlockingCommand(c, cctx, req)
		}
		if proto.IsBlockingXREAD(req) {
			return h.dispatchBlockingCommand(c, cctx, req)
		}
		return h.routeToBackend(c, cctx, req)
	}
}

func (h *ProxyHandler) handleHELLO(c gnet.Conn, cctx *clientCtx, req *proto.Request) gnet.Action {
	// HELLO [protover [AUTH username password] [SETNAME clientname]]
	// We only support RESP2 (proto 2). Reject RESP3 requests.
	if len(req.Args) >= 2 {
		proto3 := string(req.Args[1])
		if proto3 == "3" {
			h.writeImmediate(c, cctx, proto.EncodeError("NOPROTO unsupported protocol version"))
			return gnet.None
		}
		if proto3 != "2" && proto3 != "" {
			h.writeImmediate(c, cctx, proto.EncodeError("ERR Protocol version must be 2 or 3"))
			return gnet.None
		}

		for i := 2; i < len(req.Args); i++ {
			if strings.ToUpper(string(req.Args[i])) == "AUTH" {
				if i+2 >= len(req.Args) {
					h.writeImmediate(c, cctx, proto.EncodeError("ERR Syntax error in HELLO option 'auth'"))
					return gnet.None
				}
				authReq := &proto.Request{
					Args: [][]byte{[]byte("AUTH"), req.Args[i+1], req.Args[i+2]},
					Cmd:  "AUTH",
				}
				if a := h.handleAUTH(c, cctx, authReq); a != gnet.None {
					return a
				}
				if !cctx.authenticated {
					return gnet.None
				}
				i += 2
			} else if strings.ToUpper(string(req.Args[i])) == "SETNAME" {
				if i+1 >= len(req.Args) {
					h.writeImmediate(c, cctx, proto.EncodeError("ERR Syntax error in HELLO option 'setname'"))
					return gnet.None
				}
				cctx.clientName = string(req.Args[i+1])
				i++
			}
		}
	}

	if !cctx.authenticated {
		h.writeImmediate(c, cctx, proto.EncodeError("NOAUTH Authentication required."))
		return gnet.None
	}

	// Return server info as a flat array (RESP2 map representation)
	listenAddr := h.listenAddr
	if listenAddr == "" {
		listenAddr = "127.0.0.1:7777"
	}
	resp := []*proto.Value{
		proto.BulkValue([]byte("server")),  proto.BulkValue([]byte("pluster")),
		proto.BulkValue([]byte("version")), proto.BulkValue([]byte("1.0.0")),
		proto.BulkValue([]byte("proto")),   {Type: proto.TypeInteger, Integer: 2},
		proto.BulkValue([]byte("id")),      {Type: proto.TypeInteger, Integer: int64(cctx.id)},
		proto.BulkValue([]byte("mode")),    proto.BulkValue([]byte("cluster")),
		proto.BulkValue([]byte("role")),    proto.BulkValue([]byte("master")),
		proto.BulkValue([]byte("modules")), {Type: proto.TypeArray, Array: []*proto.Value{}},
	}
	h.writeImmediate(c, cctx, proto.EncodeValue(&proto.Value{Type: proto.TypeArray, Array: resp}))
	return gnet.None
}

func (h *ProxyHandler) handleAUTH(c gnet.Conn, cctx *clientCtx, req *proto.Request) gnet.Action {
	if cctx.cfg.ClientPassword == "" {
		h.writeImmediate(c, cctx, proto.EncodeError("ERR Client sent AUTH, but no password is set. Did you mean ACL SETUSER with >password?"))
		return gnet.None
	}
	var provided string
	switch len(req.Args) {
	case 2:
		provided = string(req.Args[1])
	case 3:
		provided = string(req.Args[2])
	default:
		h.writeImmediate(c, cctx, proto.EncodeError("ERR wrong number of arguments for 'auth' command"))
		return gnet.None
	}
	if provided != cctx.cfg.ClientPassword {
		cctx.authenticated = false
		h.writeImmediate(c, cctx, proto.EncodeError("WRONGPASS invalid username-password pair or user is disabled."))
		return gnet.None
	}
	cctx.authenticated = true
	h.writeImmediate(c, cctx, proto.EncodeSimpleString("OK"))
	return gnet.None
}

func (h *ProxyHandler) handleCLUSTER(c gnet.Conn, cctx *clientCtx, req *proto.Request) gnet.Action {
	if len(req.Args) < 2 {
		h.writeImmediate(c, cctx, proto.EncodeError("ERR wrong number of arguments for 'cluster' command"))
		return gnet.None
	}
	sub := strings.ToUpper(string(req.Args[1]))
	switch sub {
	case "NODES":
		return h.handleClusterNodes(c, cctx)
	case "SLOTS", "INFO", "MYID", "SHARDS", "KEYSLOT", "COUNTKEYSINSLOT", "GETKEYSINSLOT", "RESET":
		return h.handleViaRouter(c, cctx, req)
	case "CONNECTION":
		return h.handleClusterConnection(c, cctx)
	default:
		h.writeImmediate(c, cctx, proto.EncodeError(fmt.Sprintf("ERR unknown subcommand '%s'. Try CLUSTER HELP.", strings.ToLower(sub))))
		return gnet.None
	}
}

func (h *ProxyHandler) handleClusterNodes(c gnet.Conn, cctx *clientCtx) gnet.Action {
	proxyAddr := h.listenAddr
	if proxyAddr == "" {
		proxyAddr = "127.0.0.1:7777"
	}
	nodeID := "0000000000000000000000000000000000000000"
	nodesOutput := fmt.Sprintf("%s %s@0 myself,master - 0 0 1 connected 0-16383\n", nodeID, proxyAddr)
	h.writeImmediate(c, cctx, proto.EncodeBulkString([]byte(nodesOutput)))
	return gnet.None
}

func (h *ProxyHandler) handleClusterConnection(c gnet.Conn, cctx *clientCtx) gnet.Action {
	topo := h.topoMgr.LoadTopo()
	if topo == nil {
		h.writeImmediate(c, cctx, proto.EncodeError("ERR cluster topology not available"))
		return gnet.None
	}

	muxCounts := h.MuxConnCountByAddr()
	poolCounts := h.router.PoolMgr().ConnCountByAddr()
	pipeCounts := h.router.PipeMgr().ConnCountByAddr()

	totalConnByAddr := func(addr string) int {
		return muxCounts[addr] + poolCounts[addr] + pipeCounts[addr]
	}

	masters := topo.AllMasters()
	var sb strings.Builder
	for _, master := range masters {
		fmt.Fprintf(&sb, "master_%s connection: %d\n", master.Addr, totalConnByAddr(master.Addr))
		for _, replica := range master.Replicas {
			fmt.Fprintf(&sb, " slave_node_%s connection: %d\n", replica.Addr, totalConnByAddr(replica.Addr))
		}
	}

	h.writeImmediate(c, cctx, proto.EncodeBulkString([]byte(sb.String())))
	return gnet.None
}

func (h *ProxyHandler) handleClientCmd(c gnet.Conn, cctx *clientCtx, req *proto.Request) gnet.Action {
	if len(req.Args) < 2 {
		h.writeImmediate(c, cctx, proto.EncodeError("ERR wrong number of arguments for 'client' command"))
		return gnet.None
	}
	sub := strings.ToUpper(string(req.Args[1]))
	switch sub {
	case "SETNAME":
		if len(req.Args) < 3 {
			h.writeImmediate(c, cctx, proto.EncodeError("ERR wrong number of arguments for 'client|setname' command"))
			return gnet.None
		}
		if len(req.Args[2]) > maxClientNameLen {
			h.writeImmediate(c, cctx, proto.EncodeError(fmt.Sprintf("ERR Client setname command name too long, maximum is %d", maxClientNameLen)))
			return gnet.None
		}
		cctx.clientName = string(req.Args[2])
		h.writeImmediate(c, cctx, proto.EncodeSimpleString("OK"))
		return gnet.None
	case "GETNAME":
		name := cctx.clientName
		if name == "" {
			name = fmt.Sprintf("pluster-client-%d", cctx.id)
		}
		h.writeImmediate(c, cctx, proto.EncodeBulkString([]byte(name)))
		return gnet.None
	case "ID":
		h.writeImmediate(c, cctx, proto.EncodeValue(proto.IntValue(int64(cctx.id))))
		return gnet.None
	case "INFO":
		info := fmt.Sprintf("id=%d addr=%s\n", cctx.id, c.RemoteAddr())
		h.writeImmediate(c, cctx, proto.EncodeBulkString([]byte(info)))
		return gnet.None
	case "LIST":
		info := fmt.Sprintf("id=%d addr=%s\n", cctx.id, c.RemoteAddr())
		h.writeImmediate(c, cctx, proto.EncodeBulkString([]byte(info)))
		return gnet.None
	case "NO-EVICT", "NO-TOUCH", "CACHING", "UNPAUSE", "PAUSE":
		h.writeImmediate(c, cctx, proto.EncodeSimpleString("OK"))
		return gnet.None
	default:
		h.writeImmediate(c, cctx, proto.EncodeError(fmt.Sprintf("ERR unknown subcommand '%s' for 'client' command", sub)))
		return gnet.None
	}
}

func (h *ProxyHandler) handleProxyCmd(c gnet.Conn, cctx *clientCtx, req *proto.Request) gnet.Action {
	if len(req.Args) < 2 {
		h.writeImmediate(c, cctx, proto.EncodeError("ERR wrong number of arguments for 'proxy' command"))
		return gnet.None
	}
	sub := strings.ToUpper(string(req.Args[1]))
	switch sub {
	case "CONFIG":
		if len(req.Args) < 3 {
			h.writeImmediate(c, cctx, proto.EncodeError("ERR wrong number of arguments for 'proxy config' command"))
			return gnet.None
		}
		action := strings.ToUpper(string(req.Args[2]))
		switch action {
		case "GET":
			if len(req.Args) < 4 {
				h.writeImmediate(c, cctx, proto.EncodeError("ERR wrong number of arguments"))
				return gnet.None
			}
			opt := strings.ToLower(string(req.Args[3]))
			switch opt {
			case "max-clients":
				arr := []*proto.Value{
					proto.BulkValue([]byte("max-clients")),
					proto.BulkValue([]byte(fmt.Sprintf("%d", h.MaxClients()))),
				}
				h.writeImmediate(c, cctx, proto.EncodeValue(&proto.Value{Type: proto.TypeArray, Array: arr}))
				return gnet.None
			default:
				h.writeImmediate(c, cctx, proto.EncodeError(fmt.Sprintf("ERR unknown option '%s'", opt)))
				return gnet.None
			}
		case "SET":
			if len(req.Args) < 5 {
				h.writeImmediate(c, cctx, proto.EncodeError("ERR wrong number of arguments"))
				return gnet.None
			}
			opt := strings.ToLower(string(req.Args[3]))
			val := string(req.Args[4])
			switch opt {
			case "max-clients":
				n, err := strconv.ParseInt(val, 10, 64)
				if err != nil || n < 0 {
					h.writeImmediate(c, cctx, proto.EncodeError("ERR invalid value for max-clients"))
					return gnet.None
				}
				h.SetMaxClients(n)
				h.writeImmediate(c, cctx, proto.EncodeSimpleString("OK"))
				return gnet.None
			default:
				h.writeImmediate(c, cctx, proto.EncodeError(fmt.Sprintf("ERR unknown option '%s'", opt)))
				return gnet.None
			}
		default:
			h.writeImmediate(c, cctx, proto.EncodeError(fmt.Sprintf("ERR unknown action '%s'", action)))
			return gnet.None
		}
	case "INFO":
		info := "proxy_version:1.0.0\r\ncross_slot:true\r\nmultiplexing_api:off\r\n"
		h.writeImmediate(c, cctx, proto.EncodeBulkString([]byte(info)))
		return gnet.None

	case "STATS":
		snap := h.stats.Snapshot()
		h.writeImmediate(c, cctx, proto.EncodeBulkString([]byte(snap.Format())))
		return gnet.None

	case "NODES":
		proxyAddr := h.listenAddr
		if proxyAddr == "" {
			proxyAddr = "127.0.0.1:7777"
		}
		nodeID := "0000000000000000000000000000000000000000"
		nodesOutput := fmt.Sprintf("%s %s@0 myself,master - 0 0 1 connected 0-16383\n", nodeID, proxyAddr)
		h.writeImmediate(c, cctx, proto.EncodeBulkString([]byte(nodesOutput)))
		return gnet.None

	case "MULTIPLEXING":
		if len(req.Args) < 3 {
			h.writeImmediate(c, cctx, proto.EncodeError("ERR wrong number of arguments for 'proxy multiplexing' command"))
			return gnet.None
		}
		action := strings.ToUpper(string(req.Args[2]))
		switch action {
		case "STATUS":
			h.writeImmediate(c, cctx, proto.EncodeBulkString([]byte("off")))
			return gnet.None
		case "OFF":
			h.writeImmediate(c, cctx, proto.EncodeSimpleString("OK"))
			return gnet.None
		default:
			h.writeImmediate(c, cctx, proto.EncodeError(fmt.Sprintf("ERR unknown subcommand '%s'. Try PROXY MULTIPLEXING STATUS|OFF.", strings.ToLower(action))))
			return gnet.None
		}
	default:
		h.writeImmediate(c, cctx, proto.EncodeError(fmt.Sprintf("ERR unknown subcommand '%s' for 'proxy' command", sub)))
		return gnet.None
	}
}

func (h *ProxyHandler) routeToBackend(c gnet.Conn, cctx *clientCtx, req *proto.Request) gnet.Action {
	topo := h.topoMgr.LoadTopo()
	if topo == nil {
		h.writeImmediate(c, cctx, proto.EncodeError("ERR cluster topology not available"))
		return gnet.None
	}

	key, info := proto.GetFirstKeyAndInfo(req)
	if key == nil {
		if info != nil && info.IsNotAllowed() {
			h.writeImmediate(c, cctx, proto.EncodeError(fmt.Sprintf("ERR command '%s' is not supported in cluster proxy", strings.ToLower(req.Cmd))))
			return gnet.None
		}
		return h.submitToRouter(c, cctx, func(ctx context.Context) (*proto.Value, error) {
			return cctx.router.Execute(ctx, req)
		})
	}

	if info != nil && info.IsNotAllowed() {
		h.writeImmediate(c, cctx, proto.EncodeError(fmt.Sprintf("ERR command '%s' is not supported in cluster proxy", strings.ToLower(req.Cmd))))
		return gnet.None
	}
	if info != nil && info.IsAllNodes() {
		return h.submitToRouter(c, cctx, func(ctx context.Context) (*proto.Value, error) {
			return cctx.router.Execute(ctx, req)
		})
	}
	if info != nil && info.IsMultiKey() {
		switch req.Cmd {
		case "MGET", "MSET", "DEL", "UNLINK", "EXISTS", "TOUCH":
			return h.routeMultiKeyToMux(c, cctx, req, req.Cmd)
		default:
			return h.submitToRouter(c, cctx, func(ctx context.Context) (*proto.Value, error) {
				return cctx.router.Execute(ctx, req)
			})
		}
	}

	slot := proto.HashSlot(key)
	isRead := info != nil && info.IsRead() && !info.IsWrite()
	nodeAddr, nodeErr := cctx.router.NodeAddrForSlot(slot, isRead)
	if nodeErr != nil {
		h.writeImmediate(c, cctx, proto.EncodeError(fmt.Sprintf("ERR no node for slot %d", slot)))
		return gnet.None
	}

	slotIdx := h.allocResponseSlot(cctx)
	mux := cctx.mux
	mux.send(nodeAddr, pendingClientReq{
		clientConn: c,
		slotIdx:    slotIdx,
		depth:      0,
		req:        req,
	})
	return gnet.None
}

func (h *ProxyHandler) submitToRouter(
	c gnet.Conn,
	cctx *clientCtx,
	fn func(ctx context.Context) (*proto.Value, error),
) gnet.Action {
	slotIdx := h.allocResponseSlot(cctx)
	_ = h.goPool.Submit(func() {
		v, err := fn(context.Background())
		var data []byte
		if err != nil {
			data = proto.EncodeError(err.Error())
		} else {
			data = proto.EncodeValue(v)
		}
		h.fillResponseSlot(c, cctx, slotIdx, data)
	})
	return gnet.None
}

func (h *ProxyHandler) handleViaRouter(c gnet.Conn, cctx *clientCtx, req *proto.Request) gnet.Action {
	return h.submitToRouter(c, cctx, func(ctx context.Context) (*proto.Value, error) {
		return cctx.router.Execute(ctx, req)
	})
}

func (h *ProxyHandler) routeMultiKeyToMux(c gnet.Conn, cctx *clientCtx, req *proto.Request, name string) gnet.Action {
	topo := h.topoMgr.LoadTopo()
	if topo == nil {
		h.writeImmediate(c, cctx, proto.EncodeError("ERR cluster topology not available"))
		return gnet.None
	}

	type slotGroup struct {
		addr    string
		indices []int
		keys    [][]byte
	}

	var keys [][]byte
	switch name {
	case "MGET", "DEL", "UNLINK", "EXISTS", "TOUCH":
		keys = req.Args[1:]
	case "MSET":
		for i := 1; i < len(req.Args); i += 2 {
			keys = append(keys, req.Args[i])
		}
	}
	if len(keys) == 0 {
		h.writeImmediate(c, cctx, proto.EncodeError("ERR wrong number of arguments for '"+strings.ToLower(name)+"' command"))
		return gnet.None
	}

	// Group by slot (not node — Redis requires same slot within one command).
	slotMap := make(map[int]*slotGroup, 8)
	slotOrder := make([]int, 0, 8)
	for i, k := range keys {
		slot := proto.HashSlot(k)
		if sg, ok := slotMap[slot]; ok {
			sg.indices = append(sg.indices, i)
			sg.keys = append(sg.keys, k)
		} else {
			node := topo.GetNodeForSlot(slot)
			if node == nil {
				h.writeImmediate(c, cctx, proto.EncodeError(fmt.Sprintf("ERR no node for slot %d", slot)))
				return gnet.None
			}
			slotMap[slot] = &slotGroup{addr: node.Addr, indices: []int{i}, keys: [][]byte{k}}
			slotOrder = append(slotOrder, slot)
		}
	}

	// Single slot: send the original request as-is, no fan-in needed.
	if len(slotOrder) == 1 {
		slotIdx := h.allocResponseSlot(cctx)
		cctx.mux.send(slotMap[slotOrder[0]].addr, pendingClientReq{
			clientConn: c,
			slotIdx:    slotIdx,
			req:        req,
		})
		return gnet.None
	}

	groups := make([]*slotGroup, len(slotOrder))
	for i, s := range slotOrder {
		groups[i] = slotMap[s]
	}
	nGroups := len(groups)

	var mergeFunc func([]subResponse) []byte
	switch name {
	case "MGET":
		totalKeys := len(keys)
		mergeFunc = func(subs []subResponse) []byte {
			results := make([][]byte, totalKeys)
			for gi, sg := range groups {
				v, _, err := proto.ParseValue(subs[gi].data)
				if err != nil {
					return proto.EncodeError(err.Error())
				}
				if v.IsError() {
					return subs[gi].data
				}
				if v.Type == proto.TypeArray {
					for j, origIdx := range sg.indices {
						if j < len(v.Array) {
							results[origIdx] = proto.EncodeValue(v.Array[j])
						}
					}
				}
			}
			buf := []byte{'*'}
			buf = strconv.AppendInt(buf, int64(totalKeys), 10)
			buf = append(buf, '\r', '\n')
			for _, r := range results {
				if r == nil {
					buf = append(buf, '$', '-', '1', '\r', '\n')
				} else {
					buf = append(buf, r...)
				}
			}
			return buf
		}
	case "MSET":
		mergeFunc = func(subs []subResponse) []byte {
			for _, sub := range subs {
				v, _, err := proto.ParseValue(sub.data)
				if err != nil {
					return proto.EncodeError(err.Error())
				}
				if v != nil && v.IsError() {
					return sub.data
				}
			}
			return proto.EncodeSimpleString("OK")
		}
	default: // DEL, UNLINK, EXISTS, TOUCH
		mergeFunc = func(subs []subResponse) []byte {
			var total int64
			for _, sub := range subs {
				v, _, err := proto.ParseValue(sub.data)
				if err != nil {
					return proto.EncodeError(err.Error())
				}
				if v != nil && v.IsError() {
					return sub.data
				}
				if v != nil && v.Type == proto.TypeInteger {
					total += v.Integer
				}
			}
			return proto.EncodeInteger(total)
		}
	}

	slotIdx := h.allocResponseSlot(cctx)
	fi := &fanInState{
		total:     nGroups,
		subData:   make([]subResponse, nGroups),
		mergeFunc: mergeFunc,
	}
	relIdx := slotIdx - cctx.respBase
	cctx.respQueue[relIdx].fanIn = fi

	for gi, sg := range groups {
		var subArgs [][]byte
		if name == "MSET" {
			subArgs = make([][]byte, 1+len(sg.keys)*2)
			subArgs[0] = req.Args[0]
			for j, k := range sg.keys {
				subArgs[1+j*2] = k
				subArgs[2+j*2] = req.Args[1+sg.indices[j]*2+1]
			}
		} else {
			subArgs = make([][]byte, 1+len(sg.keys))
			subArgs[0] = req.Args[0]
			copy(subArgs[1:], sg.keys)
		}
		subReq := &proto.Request{Args: subArgs, Cmd: req.Cmd}
		cctx.mux.send(sg.addr, pendingClientReq{
			clientConn: c,
			slotIdx:    slotIdx,
			req:        subReq,
			fanIn:      fi,
			fanInIdx:   gi,
		})
	}
	return gnet.None
}

func (h *ProxyHandler) handleViaRouterScript(c gnet.Conn, cctx *clientCtx, req *proto.Request) gnet.Action {
	return h.submitToRouter(c, cctx, func(ctx context.Context) (*proto.Value, error) {
		return cctx.router.ExecScript(ctx, req)
	})
}


