package engine

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	gnet "github.com/panjf2000/gnet/v2"

	"github.com/pluster/pluster/pkg/proto"
)

func (h *ProxyHandler) dispatchBlockingCommand(c gnet.Conn, cctx *clientCtx, req *proto.Request) gnet.Action {
	keys := extractBlockingKeys(req)
	if len(keys) == 0 {
		h.writeImmediate(c, cctx, proto.EncodeError("ERR wrong number of arguments for '"+strings.ToLower(req.Cmd)+"' command"))
		return gnet.None
	}

	slot := proto.HashSlot(keys[0])
	for _, k := range keys[1:] {
		if proto.HashSlot(k) != slot {
			h.writeImmediate(c, cctx, proto.EncodeError("CROSSSLOT Keys in request don't hash to the same slot"))
			return gnet.None
		}
	}

	topo := h.topoMgr.LoadTopo()
	if topo == nil {
		h.writeImmediate(c, cctx, proto.EncodeError("ERR cluster topology not available"))
		return gnet.None
	}
	node := topo.GetNodeForSlot(slot)
	if node == nil {
		h.writeImmediate(c, cctx, proto.EncodeError(fmt.Sprintf("ERR no node for slot %d", slot)))
		return gnet.None
	}

	slotIdx := h.allocResponseSlot(cctx)
	cctx.state = stateBlocking
	cctx.blockingSlotIdx = slotIdx

	h.dialBlockingBackend(c, cctx, node.Addr, req)
	return gnet.None
}

func extractBlockingKeys(req *proto.Request) [][]byte {
	switch req.Cmd {
	case "BLPOP", "BRPOP", "BZPOPMIN", "BZPOPMAX":
		if len(req.Args) < 3 {
			return nil
		}
		return req.Args[1 : len(req.Args)-1]
	case "BLMOVE":
		if len(req.Args) < 6 {
			return nil
		}
		return req.Args[1:3]
	case "BLMPOP", "BZMPOP":
		if len(req.Args) < 5 {
			return nil
		}
		numKeys, err := strconv.Atoi(string(req.Args[2]))
		if err != nil || numKeys <= 0 {
			return nil
		}
		start := 3
		end := start + numKeys
		if end >= len(req.Args) {
			return nil
		}
		return req.Args[start:end]
	case "XREAD", "XREADGROUP":
		return extractXREADKeys(req)
	}
	return nil
}

func extractXREADKeys(req *proto.Request) [][]byte {
	for i, arg := range req.Args {
		if bytes.EqualFold(arg, []byte("STREAMS")) {
			remaining := req.Args[i+1:]
			if len(remaining) == 0 || len(remaining)%2 != 0 {
				return nil
			}
			return remaining[:len(remaining)/2]
		}
	}
	return nil
}

func (h *ProxyHandler) dialBlockingBackend(clientConn gnet.Conn, cctx *clientCtx, addr string, req *proto.Request) {
	pool := h.getBlockingPool(clientConn.EventLoop())
	if idle := pool.get(addr); idle != nil {
		h.reuseBlockingConn(idle, clientConn, cctx, addr, req)
		return
	}

	tcpAddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		h.fillResponseSlotInLoop(clientConn, cctx, cctx.blockingSlotIdx, proto.EncodeError(fmt.Sprintf("ERR failed to resolve %s: %v", addr, err)))
		cctx.state = stateNormal
		return
	}

	bctx := &backendCtx{
		clientConn:         clientConn,
		addr:               addr,
		isDedicated:        true,
		isSubConn:          false,
		isBlockingConn:     true,
		pendingBlockingReq: req,
	}

	ctx := gnet.NewContext(context.Background(), bctx)
	resCh, err := clientConn.EventLoop().Register(ctx, tcpAddr)
	if err != nil {
		h.fillResponseSlotInLoop(clientConn, cctx, cctx.blockingSlotIdx, proto.EncodeError(fmt.Sprintf("ERR failed to register backend %s: %v", addr, err)))
		cctx.state = stateNormal
		return
	}

	_ = h.goPool.Submit(func() {
		res := <-resCh
		if res.Err != nil {
			_ = clientConn.EventLoop().Execute(context.Background(), gnet.RunnableFunc(func(_ context.Context) error {
				cc, ok := clientConn.Context().(*clientCtx)
				if !ok {
					return nil
				}
				data := proto.EncodeError(fmt.Sprintf("ERR blocking backend connection failed: %v", res.Err))
				h.fillResponseSlotInLoop(clientConn, cc, cc.blockingSlotIdx, data)
				cc.state = stateNormal
				cc.blockingBackend = nil
				h.drainPendingCmds(clientConn, cc)
				return nil
			}))
		}
	})
}

func (h *ProxyHandler) reuseBlockingConn(backendConn gnet.Conn, clientConn gnet.Conn, cctx *clientCtx, addr string, req *proto.Request) {
	bctx, ok := backendConn.Context().(*backendCtx)
	if !ok || bctx.isClosed {
		h.dialBlockingBackend(clientConn, cctx, addr, req)
		return
	}

	bctx.clientConn = clientConn
	bctx.pending = bctx.pending[:0]
	bctx.partial = bctx.partial[:0]

	cctx.blockingBackend = backendConn

	encoded := proto.EncodeCommandBytes(req.Args...)
	bctx.pending = append(bctx.pending, backendPending{
		clientConn: clientConn,
		respond: func(v *proto.Value) {
			h.handleBlockingResponse(clientConn, cctx, backendConn, v, req)
		},
	})
	_, _ = backendConn.Write(encoded)
}

func (h *ProxyHandler) handleBlockingResponse(clientConn gnet.Conn, cctx *clientCtx, backendConn gnet.Conn, v *proto.Value, req *proto.Request) {
	if v.IsMovedError() {
		_, newAddr, err := v.ParseRedirection()
		if err == nil {
			h.topoMgr.TriggerRefresh()
			cctx.blockingBackend = nil
			_ = clientConn.EventLoop().Close(backendConn)
			h.dialBlockingBackend(clientConn, cctx, newAddr, req)
			return
		}
	}

	data := proto.EncodeValue(v)
	h.fillResponseSlotInLoop(clientConn, cctx, cctx.blockingSlotIdx, data)

	cctx.state = stateNormal
	cctx.blockingBackend = nil

	h.returnBlockingConn(clientConn.EventLoop(), backendConn)

	h.drainPendingCmds(clientConn, cctx)
}

func (h *ProxyHandler) returnBlockingConn(el gnet.EventLoop, backendConn gnet.Conn) {
	bctx, ok := backendConn.Context().(*backendCtx)
	if !ok || len(bctx.pending) > 0 {
		_ = el.Close(backendConn)
		return
	}

	pool := h.getBlockingPool(el)
	if !pool.put(bctx.addr, backendConn) {
		_ = el.Close(backendConn)
	}
}

func (h *ProxyHandler) drainPendingCmds(c gnet.Conn, cctx *clientCtx) {
	if len(cctx.pendingCmds) == 0 {
		return
	}
	cmds := cctx.pendingCmds
	cctx.pendingCmds = nil
	mux := h.getMux(c.EventLoop())
	for _, req := range cmds {
		if a := h.dispatchClientRequest(c, cctx, req); a != gnet.None {
			break
		}
	}
	mux.flushBackendWrites()
}
