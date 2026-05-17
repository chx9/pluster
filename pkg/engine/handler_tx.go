package engine

import (
	"context"
	"fmt"
	"net"
	"strings"

	gnet "github.com/panjf2000/gnet/v2"

	"github.com/pluster/pluster/pkg/proto"
)

func (h *ProxyHandler) handleWATCH(c gnet.Conn, cctx *clientCtx, req *proto.Request) gnet.Action {
	if len(req.Args) < 2 {
		h.writeImmediate(c, cctx, proto.EncodeError("ERR wrong number of arguments for 'watch' command"))
		return gnet.None
	}
	if cctx.state == stateMulti || cctx.state == stateWatchMulti {
		h.writeImmediate(c, cctx, proto.EncodeError("ERR WATCH inside MULTI is not allowed"))
		return gnet.None
	}

	keys := req.Args[1:]
	slot := proto.HashSlot(keys[0])
	for _, k := range keys[1:] {
		if proto.HashSlot(k) != slot {
			h.writeImmediate(c, cctx, proto.EncodeError("CROSSSLOT Keys in request don't hash to the same slot"))
			return gnet.None
		}
	}

	topo := h.topoMgr.LoadTopo()
	node := topo.GetNodeForSlot(slot)
	if node == nil {
		h.writeImmediate(c, cctx, proto.EncodeError(fmt.Sprintf("ERR no node for slot %d", slot)))
		return gnet.None
	}
	addr := node.Addr

	slotIdx := h.allocResponseSlot(cctx)

	if cctx.watchBackend != nil && cctx.watchAddr == addr {
		bctx := cctx.watchBackend.Context().(*backendCtx)
		encoded := proto.EncodeCommandBytes(req.Args...)
		bctx.pending = append(bctx.pending, backendPending{
			clientConn: c,
			respond: func(v *proto.Value) {
				if !v.IsError() {
					cctx.state = stateWatch
				}
				h.fillResponseSlotInLoop(c, cctx, slotIdx, proto.EncodeValue(v))
			},
		})
		_, _ = cctx.watchBackend.Write(encoded)
		return gnet.None
	}

	if cctx.watchBackend != nil {
		_ = c.EventLoop().Close(cctx.watchBackend)
		cctx.watchBackend = nil
	}

	cctx.watchAddr = addr
	cctx.pendingWatchReq = req
	cctx.pendingWatchSlot = slotIdx
	h.dialDedicatedBackend(c, cctx, addr, false, req)
	return gnet.None
}

func (h *ProxyHandler) handleUNWATCH(c gnet.Conn, cctx *clientCtx) gnet.Action {
	if cctx.watchBackend != nil {
		bctx := cctx.watchBackend.Context().(*backendCtx)
		encoded := proto.EncodeCommandBytes([]byte("UNWATCH"))
		bctx.pending = append(bctx.pending, backendPending{
			clientConn: c,
			respond:    func(_ *proto.Value) {},
		})
		_, _ = cctx.watchBackend.Write(encoded)
		_ = c.EventLoop().Close(cctx.watchBackend)
		cctx.watchBackend = nil
		cctx.watchAddr = ""
	}
	cctx.state = stateNormal
	h.writeImmediate(c, cctx, proto.EncodeSimpleString("OK"))
	return gnet.None
}

func (h *ProxyHandler) handleInMulti(c gnet.Conn, cctx *clientCtx, req *proto.Request, name string) gnet.Action {
	switch name {
	case "EXEC":
		return h.execMulti(c, cctx)
	case "DISCARD":
		return h.discardMulti(c, cctx)
	case "MULTI":
		if cctx.state == stateWatch {
			cctx.state = stateWatchMulti
			cctx.txQueue = cctx.txQueue[:0]
			cctx.txErr = false
			h.writeImmediate(c, cctx, proto.EncodeSimpleString("OK"))
			return gnet.None
		}
		h.writeImmediate(c, cctx, proto.EncodeError("ERR MULTI calls can not be nested"))
		return gnet.None
	case "WATCH":
		h.writeImmediate(c, cctx, proto.EncodeError("ERR WATCH inside MULTI is not allowed"))
		return gnet.None
	case "UNWATCH":
		h.writeImmediate(c, cctx, proto.EncodeError("ERR UNWATCH not allowed inside MULTI"))
		return gnet.None
	case "RESET":
		h.resetClientState(c, cctx)
		h.writeImmediate(c, cctx, proto.EncodeSimpleString("RESET"))
		return gnet.None
	case "QUIT":
		_, _ = c.Write(proto.EncodeSimpleString("OK"))
		return gnet.Close
	default:
		info := proto.GetCmdInfo(name)
		if info == nil {
			cctx.txErr = true
			h.writeImmediate(c, cctx, proto.EncodeError(fmt.Sprintf("ERR unknown command '%s'", strings.ToLower(name))))
			return gnet.None
		}
		if info.IsNotAllowed() {
			cctx.txErr = true
			h.writeImmediate(c, cctx, proto.EncodeError(fmt.Sprintf("ERR command '%s' is not supported in cluster proxy", strings.ToLower(name))))
			return gnet.None
		}
		cctx.txQueue = append(cctx.txQueue, req)
		h.writeImmediate(c, cctx, proto.EncodeSimpleString("QUEUED"))
		return gnet.None
	}
}

func (h *ProxyHandler) execMulti(c gnet.Conn, cctx *clientCtx) gnet.Action {
	if cctx.txErr {
		cctx.state = stateNormal
		cctx.txQueue = nil
		cctx.txErr = false
		if cctx.watchBackend != nil {
			_ = c.EventLoop().Close(cctx.watchBackend)
			cctx.watchBackend = nil
			cctx.watchAddr = ""
		}
		if cctx.multiBackend != nil {
			_ = c.EventLoop().Close(cctx.multiBackend)
			cctx.multiBackend = nil
		}
		h.writeImmediate(c, cctx, proto.EncodeError("EXECABORT Transaction discarded because of previous errors."))
		return gnet.None
	}

	if cctx.state == stateWatchMulti && cctx.watchBackend != nil {
		return h.execMultiOnDedicatedConn(c, cctx, cctx.watchBackend, true)
	}

	queue := cctx.txQueue
	if len(queue) == 0 {
		cctx.state = stateNormal
		cctx.txQueue = nil
		cctx.txErr = false
		_, _ = c.Write(proto.EncodeValue(&proto.Value{Type: proto.TypeArray, Array: []*proto.Value{}}))
		return gnet.None
	}

	_, addr, err := h.resolveMultiSlot(cctx, queue)
	if err != nil {
		cctx.state = stateNormal
		cctx.txQueue = nil
		cctx.txErr = false
		h.writeImmediate(c, cctx, proto.EncodeError(err.Error()))
		return gnet.None
	}

	if cctx.multiBackend != nil {
		bctx := cctx.multiBackend.Context().(*backendCtx)
		if bctx.addr == addr {
			return h.execMultiOnDedicatedConn(c, cctx, cctx.multiBackend, false)
		}
		_ = c.EventLoop().Close(cctx.multiBackend)
		cctx.multiBackend = nil
	}

	cctx.txQueue = nil
	cctx.txErr = false
	slotIdx := h.allocResponseSlot(cctx)
	cctx.state = stateNormal

	h.dialMultiBackend(c, cctx, addr, queue, slotIdx)
	return gnet.None
}

func (h *ProxyHandler) execMultiOnDedicatedConn(c gnet.Conn, cctx *clientCtx, dedicatedConn gnet.Conn, closeAfter bool) gnet.Action {
	bctx := dedicatedConn.Context().(*backendCtx)
	queue := cctx.txQueue

	cctx.state = stateNormal
	cctx.txQueue = nil
	cctx.txErr = false
	if closeAfter {
		cctx.watchBackend = nil
		cctx.watchAddr = ""
	} else {
		cctx.multiBackend = nil
	}

	slotIdx := h.allocResponseSlot(cctx)

	multiCmd := proto.EncodeCommandBytes([]byte("MULTI"))
	bctx.pending = append(bctx.pending, backendPending{
		clientConn: c,
		respond: func(v *proto.Value) {
			if v.IsError() {
				_ = c.EventLoop().Close(dedicatedConn)
				h.fillResponseSlotInLoop(c, cctx, slotIdx, proto.EncodeError("ERR failed to start MULTI on backend"))
				return
			}
			for _, req := range queue {
				encoded := proto.EncodeCommandBytes(req.Args...)
				bctx.pending = append(bctx.pending, backendPending{
					clientConn: c,
					respond:    func(_ *proto.Value) {},
				})
				_, _ = dedicatedConn.Write(encoded)
			}
			execCmd := proto.EncodeCommandBytes([]byte("EXEC"))
			bctx.pending = append(bctx.pending, backendPending{
				clientConn: c,
				respond: func(v *proto.Value) {
					_ = c.EventLoop().Close(dedicatedConn)
					h.fillResponseSlotInLoop(c, cctx, slotIdx, proto.EncodeValue(v))
				},
			})
			_, _ = dedicatedConn.Write(execCmd)
		},
	})
	_, _ = dedicatedConn.Write(multiCmd)
	return gnet.None
}

func (h *ProxyHandler) resolveMultiSlot(cctx *clientCtx, queue []*proto.Request) (int, string, error) {
	topo := h.topoMgr.LoadTopo()
	if topo == nil {
		return 0, "", fmt.Errorf("ERR cluster topology not available")
	}

	targetSlot := -1
	for _, req := range queue {
		key := proto.GetFirstKeyFromRequest(req)
		if key == nil {
			continue
		}
		s := proto.HashSlot(key)
		if targetSlot < 0 {
			targetSlot = s
		} else if s != targetSlot {
			return 0, "", fmt.Errorf("CROSSSLOT Keys in MULTI transaction don't hash to the same slot")
		}
	}

	if targetSlot < 0 {
		masters := topo.AllMasters()
		if len(masters) == 0 {
			return 0, "", fmt.Errorf("ERR no cluster nodes available")
		}
		return 0, masters[0].Addr, nil
	}

	node := topo.GetNodeForSlot(targetSlot)
	if node == nil {
		return 0, "", fmt.Errorf("ERR no node for slot %d", targetSlot)
	}
	return targetSlot, node.Addr, nil
}

func (h *ProxyHandler) dialMultiBackend(clientConn gnet.Conn, cctx *clientCtx, addr string, queue []*proto.Request, slotIdx int) {
	tcpAddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		h.fillResponseSlot(clientConn, cctx, slotIdx, proto.EncodeError(fmt.Sprintf("ERR failed to resolve %s: %v", addr, err)))
		return
	}

	bctx := &backendCtx{
		clientConn:  clientConn,
		addr:        addr,
		isDedicated: true,
		isMultiConn: true,
	}

	ctx := gnet.NewContext(context.Background(), bctx)
	resCh, err := clientConn.EventLoop().Register(ctx, tcpAddr)
	if err != nil {
		h.fillResponseSlot(clientConn, cctx, slotIdx, proto.EncodeError(fmt.Sprintf("ERR failed to register backend %s: %v", addr, err)))
		return
	}

	_ = h.goPool.Submit(func() {
		res := <-resCh
		if res.Err != nil {
			h.fillResponseSlot(clientConn, cctx, slotIdx, proto.EncodeError(fmt.Sprintf("ERR backend connection failed: %v", res.Err)))
			return
		}
		_ = clientConn.EventLoop().Execute(context.Background(), gnet.RunnableFunc(func(_ context.Context) error {
			cc, ok := clientConn.Context().(*clientCtx)
			if !ok {
				return nil
			}
			bc := res.Conn.Context().(*backendCtx)
			if h.cfg.Password != "" {
				_ = h.authDedicatedConn(res.Conn, cc)
			}
			multiCmd := proto.EncodeCommandBytes([]byte("MULTI"))
			bc.pending = append(bc.pending, backendPending{
				clientConn: clientConn,
				respond: func(v *proto.Value) {
					if v.IsError() {
						_ = clientConn.EventLoop().Close(res.Conn)
						h.fillResponseSlotInLoop(clientConn, cc, slotIdx, proto.EncodeError("ERR failed to start MULTI on backend"))
						return
					}
					for _, req := range queue {
						encoded := proto.EncodeCommandBytes(req.Args...)
						bc.pending = append(bc.pending, backendPending{
							clientConn: clientConn,
							respond:    func(_ *proto.Value) {},
						})
						_, _ = res.Conn.Write(encoded)
					}
					execCmd := proto.EncodeCommandBytes([]byte("EXEC"))
					bc.pending = append(bc.pending, backendPending{
						clientConn: clientConn,
						respond: func(v *proto.Value) {
							_ = clientConn.EventLoop().Close(res.Conn)
							h.fillResponseSlotInLoop(clientConn, cc, slotIdx, proto.EncodeValue(v))
						},
					})
					_, _ = res.Conn.Write(execCmd)
				},
			})
			_, _ = res.Conn.Write(multiCmd)
			return nil
		}))
	})
}

func (h *ProxyHandler) discardMulti(c gnet.Conn, cctx *clientCtx) gnet.Action {
	if cctx.watchBackend != nil {
		_ = c.EventLoop().Close(cctx.watchBackend)
		cctx.watchBackend = nil
		cctx.watchAddr = ""
	}
	if cctx.multiBackend != nil {
		_ = c.EventLoop().Close(cctx.multiBackend)
		cctx.multiBackend = nil
	}
	cctx.state = stateNormal
	cctx.txQueue = cctx.txQueue[:0]
	cctx.txErr = false
	h.writeImmediate(c, cctx, proto.EncodeSimpleString("OK"))
	return gnet.None
}
