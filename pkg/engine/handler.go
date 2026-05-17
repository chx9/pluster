package engine

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/panjf2000/ants/v2"
	gnet "github.com/panjf2000/gnet/v2"

	"github.com/pluster/pluster/pkg/cluster"
	"github.com/pluster/pluster/pkg/config"
	"github.com/pluster/pluster/pkg/proto"
	"github.com/pluster/pluster/pkg/router"
)

type clientState int

const (
	stateNormal    clientState = iota
	stateSubscribe             // in SUBSCRIBE/PSUBSCRIBE mode
	stateMulti                 // in MULTI block (no WATCH)
	stateWatch                 // in WATCH state (before MULTI)
	stateWatchMulti            // in WATCH + MULTI block
	stateBlocking              // in blocking command (dedicated conn in-flight)
)

const (
	dedicatedConnDialTimeout = 5 * time.Second
	maxClientNameLen         = 100
)

type responseSlot struct {
	ready bool
	data  []byte
	fanIn *fanInState
}

type fanInState struct {
	total     int
	received  int
	subData   []subResponse
	mergeFunc func([]subResponse) []byte
}

type subResponse struct {
	subIdx int
	data   []byte
}

type clientCtx struct {
	id                uint64
	state             clientState
	authenticated     bool
	isClient          bool
	clientName        string
	txQueue           []*proto.Request
	txErr             bool
	watchAddr         string
	watchBackend      gnet.Conn
	multiBackend      gnet.Conn
	subBackend        gnet.Conn
	blockingBackend   gnet.Conn
	blockingSlotIdx   int
	pendingCmds       []*proto.Request
	pendingWatchReq   *proto.Request
	pendingWatchSlot  int
	pendingSubReq     *proto.Request
	router            *router.Router
	cfg               *config.Config
	respQueue         []responseSlot
	respBase          int
	mux               *backendMux
}

type backendCtx struct {
	clientConn         gnet.Conn
	addr               string
	isDedicated        bool
	isSubConn          bool
	isBlockingConn     bool
	isMultiConn        bool
	isSharedHub        bool
	isClosed           bool
	pendingBlockingReq *proto.Request
	pending            []backendPending
	partial            []byte
}

type backendPending struct {
	clientConn gnet.Conn
	respond    func(v *proto.Value)
}

type ProxyHandler struct {
	gnet.BuiltinEventEngine

	listenAddr    string
	cfg           *config.Config
	topoMgr       *cluster.Manager
	router        *router.Router
	goPool        *ants.Pool
	numClients    atomic.Int64
	maxClients    atomic.Int64
	clientIDSeq   atomic.Uint64
	eng           atomic.Pointer[gnet.Engine]
	bootCh        chan struct{}
	muxes         sync.Map
	blockingPools sync.Map
	pubsubHubs    sync.Map
	stats         *Stats
	muxConnCounts sync.Map
}

func NewProxyHandler(cfg *config.Config, topoMgr *cluster.Manager, r *router.Router) (*ProxyHandler, error) {
	pool, err := ants.NewPool(4096, ants.WithNonblocking(false))
	if err != nil {
		return nil, fmt.Errorf("create goroutine pool: %w", err)
	}
	h := &ProxyHandler{
		cfg:     cfg,
		topoMgr: topoMgr,
		router:  r,
		goPool:  pool,
		bootCh:  make(chan struct{}),
		stats:   newStats(),
	}
	h.maxClients.Store(int64(cfg.MaxClients))
	return h, nil
}

func (h *ProxyHandler) SetListenAddr(addr string) {
	h.listenAddr = addr
}

func (h *ProxyHandler) getMux(el gnet.EventLoop) *backendMux {
	if v, ok := h.muxes.Load(el); ok {
		return v.(*backendMux)
	}
	m := newBackendMux(el, h)
	actual, _ := h.muxes.LoadOrStore(el, m)
	return actual.(*backendMux)
}

func (h *ProxyHandler) getPubsubHub(el gnet.EventLoop) *pubsubHub {
	if v, ok := h.pubsubHubs.Load(el); ok {
		return v.(*pubsubHub)
	}
	hub := newPubsubHub(el, h)
	actual, _ := h.pubsubHubs.LoadOrStore(el, hub)
	return actual.(*pubsubHub)
}

func (h *ProxyHandler) getBlockingPool(el gnet.EventLoop) *blockingConnPool {
	if v, ok := h.blockingPools.Load(el); ok {
		return v.(*blockingConnPool)
	}
	p := newBlockingConnPool()
	actual, _ := h.blockingPools.LoadOrStore(el, p)
	return actual.(*blockingConnPool)
}

func (h *ProxyHandler) NumClients() int64 {
	return h.numClients.Load()
}

func (h *ProxyHandler) MaxClients() int64 {
	return h.maxClients.Load()
}

func (h *ProxyHandler) SetMaxClients(n int64) {
	h.maxClients.Store(n)
}

func (h *ProxyHandler) Stats() *Stats {
	return h.stats
}

func (h *ProxyHandler) incMuxConn(addr string) {
	v, _ := h.muxConnCounts.LoadOrStore(addr, new(atomic.Int64))
	v.(*atomic.Int64).Add(1)
}

func (h *ProxyHandler) decMuxConn(addr string) {
	if v, ok := h.muxConnCounts.Load(addr); ok {
		v.(*atomic.Int64).Add(-1)
	}
}

func (h *ProxyHandler) MuxConnCountByAddr() map[string]int {
	counts := make(map[string]int)
	h.muxConnCounts.Range(func(k, v any) bool {
		n := int(v.(*atomic.Int64).Load())
		if n > 0 {
			counts[k.(string)] = n
		}
		return true
	})
	return counts
}

func (h *ProxyHandler) BlockingPoolReuses() int {
	total := 0
	h.blockingPools.Range(func(_, v any) bool {
		total += v.(*blockingConnPool).reuses
		return true
	})
	return total
}

func (h *ProxyHandler) Engine() *gnet.Engine {
	return h.eng.Load()
}

func (h *ProxyHandler) OnBoot(eng gnet.Engine) gnet.Action {
	h.eng.Store(&eng)
	slog.Info("pluster engine booted", "addr", h.listenAddr, "workers", runtime.NumCPU())
	close(h.bootCh)
	return gnet.None
}

func (h *ProxyHandler) WaitBoot() {
	<-h.bootCh
}

func (h *ProxyHandler) OnShutdown(_ gnet.Engine) {
	h.goPool.Release()
}

func (h *ProxyHandler) OnOpen(c gnet.Conn) ([]byte, gnet.Action) {
	if h.isClientConn(c) {
		return h.onClientOpen(c)
	}
	return h.onBackendOpen(c)
}

func (h *ProxyHandler) OnClose(c gnet.Conn, err error) gnet.Action {
	if h.isClientConn(c) {
		return h.onClientClose(c, err)
	}
	return h.onBackendClose(c, err)
}

func (h *ProxyHandler) OnTraffic(c gnet.Conn) gnet.Action {
	if h.isClientConn(c) {
		return h.onClientTraffic(c)
	}
	return h.onBackendTraffic(c)
}

func (h *ProxyHandler) isClientConn(c gnet.Conn) bool {
	// Fast path: context already set (OnTraffic / OnClose)
	if ctx, ok := c.Context().(*clientCtx); ok {
		return ctx.isClient
	}
	// Slow path: context not yet set (OnOpen). Use local address comparison.
	return c.LocalAddr() != nil && c.LocalAddr().String() == h.listenAddr
}

func (h *ProxyHandler) onClientOpen(c gnet.Conn) ([]byte, gnet.Action) {
	if max := h.maxClients.Load(); max > 0 && h.numClients.Load() >= max {
		return proto.EncodeError("ERR max number of clients reached"), gnet.Close
	}
	id := h.clientIDSeq.Add(1)
	h.numClients.Add(1)
	h.stats.incConnections()
	mux := h.getMux(c.EventLoop())
	cctx := &clientCtx{
		id:            id,
		state:         stateNormal,
		authenticated: h.cfg.ClientPassword == "",
		isClient:      true,
		router:        h.router,
		cfg:           h.cfg,
		mux:           mux,
		respQueue:     make([]responseSlot, 0, 16),
	}
	c.SetContext(cctx)
	return nil, gnet.None
}

func (h *ProxyHandler) onClientClose(c gnet.Conn, _ error) gnet.Action {
	h.numClients.Add(-1)
	h.stats.decConnections()
	cctx, ok := c.Context().(*clientCtx)
	if !ok {
		return gnet.None
	}
	if cctx.watchBackend != nil {
		_ = c.EventLoop().Close(cctx.watchBackend)
		cctx.watchBackend = nil
	}
	if cctx.multiBackend != nil {
		_ = c.EventLoop().Close(cctx.multiBackend)
		cctx.multiBackend = nil
	}
	if cctx.subBackend != nil {
		_ = c.EventLoop().Close(cctx.subBackend)
		cctx.subBackend = nil
	}
	if cctx.blockingBackend != nil {
		_ = c.EventLoop().Close(cctx.blockingBackend)
		cctx.blockingBackend = nil
	}
	if cctx.state == stateSubscribe {
		hub := h.getPubsubHub(c.EventLoop())
		hub.removeClient(c)
	}
	return gnet.None
}

func (h *ProxyHandler) onBackendOpen(c gnet.Conn) ([]byte, gnet.Action) {
	bctx, ok := c.Context().(*backendCtx)
	if !ok {
		return nil, gnet.Close
	}

	if !bctx.isDedicated {
		mux := h.getMux(c.EventLoop())
		mux.onOpen(c, bctx.addr)
		return nil, gnet.None
	}

	if bctx.isSharedHub {
		hub := h.getPubsubHub(c.EventLoop())
		hub.onSharedBackendOpen(c)
		return nil, gnet.None
	}

	clientConn := bctx.clientConn
	if clientConn == nil {
		return nil, gnet.None
	}

	cctx, ok := clientConn.Context().(*clientCtx)
	if !ok {
		return nil, gnet.Close
	}

	if bctx.isBlockingConn {
		cctx.blockingBackend = c
		req := bctx.pendingBlockingReq
		bctx.pendingBlockingReq = nil

		sendBlockingCmd := func() {
			if req == nil {
				return
			}
			encoded := proto.EncodeCommandBytes(req.Args...)
			bctx.pending = append(bctx.pending, backendPending{
				clientConn: clientConn,
				respond: func(v *proto.Value) {
					h.handleBlockingResponse(clientConn, cctx, c, v, req)
				},
			})
			_, _ = c.Write(encoded)
		}

		if h.cfg.Password != "" {
			if err := h.authDedicatedConn(c, cctx); err != nil {
				h.fillResponseSlotInLoop(clientConn, cctx, cctx.blockingSlotIdx, proto.EncodeError(err.Error()))
				return nil, gnet.Close
			}
			if len(bctx.pending) > 0 {
				prev := bctx.pending[len(bctx.pending)-1].respond
				bctx.pending[len(bctx.pending)-1].respond = func(v *proto.Value) {
					prev(v)
					if !v.IsError() {
						sendBlockingCmd()
					}
				}
			}
		} else {
			sendBlockingCmd()
		}
	} else if bctx.isMultiConn {
		cctx.multiBackend = c
	} else if bctx.isSubConn {
		cctx.subBackend = c
		if cctx.pendingSubReq != nil {
			req := cctx.pendingSubReq
			cctx.pendingSubReq = nil
			encoded := proto.EncodeCommandBytes(req.Args...)
			_, _ = c.Write(encoded)
		}
	} else {
		cctx.watchBackend = c
		bctx.addr = cctx.watchAddr
		if h.cfg.Password != "" {
			if err := h.authDedicatedConn(c, cctx); err != nil {
				h.fillResponseSlotInLoop(clientConn, cctx, cctx.pendingWatchSlot, proto.EncodeError(err.Error()))
				return nil, gnet.Close
			}
		}
		if cctx.pendingWatchReq != nil {
			req := cctx.pendingWatchReq
			slotIdx := cctx.pendingWatchSlot
			cctx.pendingWatchReq = nil
			encoded := proto.EncodeCommandBytes(req.Args...)
			bctx.pending = append(bctx.pending, backendPending{
				clientConn: clientConn,
				respond: func(v *proto.Value) {
					if v.IsMovedError() {
						_, newAddr, _ := v.ParseRedirection()
						cctx.watchAddr = newAddr
						cctx.watchBackend = nil
						cctx.pendingWatchReq = req
						cctx.pendingWatchSlot = slotIdx
						h.dialDedicatedBackend(clientConn, cctx, newAddr, false, req)
						return
					}
					if !v.IsError() {
						cctx.state = stateWatch
					}
					h.fillResponseSlotInLoop(clientConn, cctx, slotIdx, proto.EncodeValue(v))
					h.drainPendingCmds(clientConn, cctx)
				},
			})
			_, _ = c.Write(encoded)
		}
	}
	return nil, gnet.None
}

func (h *ProxyHandler) onBackendClose(c gnet.Conn, _ error) gnet.Action {
	bctx, ok := c.Context().(*backendCtx)
	if !ok {
		return gnet.None
	}
	if !bctx.isDedicated {
		mux := h.getMux(c.EventLoop())
		mux.onClose(bctx.addr)
		return gnet.None
	}

	if bctx.isSharedHub {
		hub := h.getPubsubHub(c.EventLoop())
		hub.onSharedBackendClose()
		return gnet.None
	}

	bctx.isClosed = true
	if bctx.clientConn != nil {
		cctx, ok := bctx.clientConn.Context().(*clientCtx)
		if ok {
			if cctx.watchBackend == c {
				cctx.watchBackend = nil
			}
			if cctx.multiBackend == c {
				cctx.multiBackend = nil
			}
			if cctx.subBackend == c {
				cctx.subBackend = nil
			}
			if cctx.blockingBackend == c {
				cctx.blockingBackend = nil
				if cctx.state == stateBlocking {
					data := proto.EncodeError("ERR blocking command interrupted: backend connection closed")
					h.fillResponseSlotInLoop(bctx.clientConn, cctx, cctx.blockingSlotIdx, data)
					cctx.state = stateNormal
					h.drainPendingCmds(bctx.clientConn, cctx)
				}
			}
		}
	}
	return gnet.None
}

func (h *ProxyHandler) onClientTraffic(c gnet.Conn) gnet.Action {
	cctx, ok := c.Context().(*clientCtx)
	if !ok {
		return gnet.Close
	}

	mux := cctx.mux
	var action gnet.Action
	for {
		buf, _ := c.Peek(-1)
		if len(buf) == 0 {
			break
		}

		req, consumed, err := proto.ParseRequest(buf)
		if err != nil {
			_, _ = c.Write(proto.EncodeError("ERR Protocol error: " + err.Error()))
			action = gnet.Close
			break
		}
		if consumed == 0 {
			break
		}
		_, _ = c.Discard(consumed)

		if a := h.dispatchClientRequest(c, cctx, req); a != gnet.None {
			action = a
			break
		}
	}

	mux.flushBackendWrites()
	if mux.localCmdCount > 0 {
		h.stats.addCommands(mux.localCmdCount)
		mux.localCmdCount = 0
	}
	return action
}

func (h *ProxyHandler) onBackendTraffic(c gnet.Conn) gnet.Action {
	bctx, ok := c.Context().(*backendCtx)
	if !ok {
		return gnet.Close
	}

	if !bctx.isDedicated {
		mux := h.getMux(c.EventLoop())
		return mux.onTraffic(c, bctx)
	}

	if bctx.isSharedHub {
		hub := h.getPubsubHub(c.EventLoop())
		return hub.onSharedBackendTraffic(c)
	}

	for {
		buf, _ := c.Peek(-1)
		if len(buf) == 0 {
			return gnet.None
		}

		val, consumed, err := proto.ParseValue(buf)
		if err != nil {
			return gnet.Close
		}
		if consumed == 0 {
			return gnet.None
		}
		_, _ = c.Discard(consumed)

		if bctx.isSubConn {
			if len(bctx.pending) > 0 {
				entry := bctx.pending[0]
				bctx.pending = bctx.pending[1:]
				entry.respond(val)
			} else if bctx.clientConn != nil {
				writeValueToClient(bctx.clientConn, val)
			}
			continue
		}

		if len(bctx.pending) == 0 {
			continue
		}
		entry := bctx.pending[0]
		bctx.pending = bctx.pending[1:]
		entry.respond(val)
	}
}

func (h *ProxyHandler) writeImmediate(c gnet.Conn, cctx *clientCtx, data []byte) {
	if len(cctx.respQueue) == 0 {
		_, _ = c.Write(data)
		return
	}
	slotIdx := h.allocResponseSlot(cctx)
	h.fillResponseSlot(c, cctx, slotIdx, data)
}

func (h *ProxyHandler) allocResponseSlot(cctx *clientCtx) int {
	absIdx := cctx.respBase + len(cctx.respQueue)
	cctx.respQueue = append(cctx.respQueue, responseSlot{})
	return absIdx
}

func (h *ProxyHandler) fillResponseSlot(c gnet.Conn, cctx *clientCtx, absIdx int, data []byte) {
	_ = c.EventLoop().Execute(context.Background(), gnet.RunnableFunc(func(_ context.Context) error {
		h.fillResponseSlotInLoop(c, cctx, absIdx, data)
		return nil
	}))
}

func (h *ProxyHandler) fillResponseSlotInLoop(c gnet.Conn, cctx *clientCtx, absIdx int, data []byte) {
	relIdx := absIdx - cctx.respBase
	if relIdx < 0 || relIdx >= len(cctx.respQueue) {
		return
	}
	cctx.respQueue[relIdx].ready = true
	cctx.respQueue[relIdx].data = data

	head := 0
	for head < len(cctx.respQueue) && cctx.respQueue[head].ready {
		head++
	}
	if head == 0 {
		return
	}

	if head == 1 {
		_, _ = c.Write(cctx.respQueue[0].data)
		cctx.respQueue[0].data = nil
	} else {
		iov := make([][]byte, head)
		for i := 0; i < head; i++ {
			iov[i] = cctx.respQueue[i].data
			cctx.respQueue[i].data = nil
		}
		_, _ = c.Writev(iov)
	}

	cctx.respQueue = cctx.respQueue[head:]
	cctx.respBase += head
	if cap(cctx.respQueue) > 256 && len(cctx.respQueue) < cap(cctx.respQueue)/4 {
		fresh := make([]responseSlot, len(cctx.respQueue), 64)
		copy(fresh, cctx.respQueue)
		cctx.respQueue = fresh
	}
}

func (h *ProxyHandler) accumulateFanInSubResponse(c gnet.Conn, cctx *clientCtx, absIdx int, subIdx int, data []byte, accum map[gnet.Conn]*connWriteAccum) {
	relIdx := absIdx - cctx.respBase
	if relIdx < 0 || relIdx >= len(cctx.respQueue) {
		return
	}
	slot := &cctx.respQueue[relIdx]
	fi := slot.fanIn
	if fi == nil {
		return
	}
	owned := make([]byte, len(data))
	copy(owned, data)
	fi.subData[subIdx] = subResponse{subIdx: subIdx, data: owned}
	fi.received++
	if fi.received < fi.total {
		return
	}
	merged := fi.mergeFunc(fi.subData)
	slot.fanIn = nil

	head := 0
	for head < len(cctx.respQueue) && cctx.respQueue[head].ready {
		head++
	}
	if relIdx == head {
		slot.ready = true
		slot.data = merged
		head = 0
		for head < len(cctx.respQueue) && cctx.respQueue[head].ready {
			head++
		}
		wa := accum[c]
		if wa == nil {
			wa = connWriteAccumPool.Get().(*connWriteAccum)
			wa.iov = wa.iov[:0]
			accum[c] = wa
		}
		for i := 0; i < head; i++ {
			wa.iov = append(wa.iov, cctx.respQueue[i].data)
			cctx.respQueue[i].data = nil
		}
		cctx.respQueue = cctx.respQueue[head:]
		cctx.respBase += head
	} else {
		slot.ready = true
		slot.data = merged
	}
	if cap(cctx.respQueue) > 256 && len(cctx.respQueue) < cap(cctx.respQueue)/4 {
		fresh := make([]responseSlot, len(cctx.respQueue), 64)
		copy(fresh, cctx.respQueue)
		cctx.respQueue = fresh
	}
}

func (h *ProxyHandler) accumulateResponseSlot(c gnet.Conn, cctx *clientCtx, absIdx int, data []byte, accum map[gnet.Conn]*connWriteAccum) {
	relIdx := absIdx - cctx.respBase
	if relIdx < 0 || relIdx >= len(cctx.respQueue) {
		return
	}
	if cctx.respQueue[relIdx].fanIn != nil {
		return
	}

	head := 0
	for head < len(cctx.respQueue) && cctx.respQueue[head].ready {
		head++
	}

	if relIdx == head {
		cctx.respQueue[relIdx].ready = true
		cctx.respQueue[relIdx].data = data

		head = 0
		for head < len(cctx.respQueue) && cctx.respQueue[head].ready {
			head++
		}

		wa := accum[c]
		if wa == nil {
			wa = connWriteAccumPool.Get().(*connWriteAccum)
			wa.iov = wa.iov[:0]
			accum[c] = wa
		}
		for i := 0; i < head; i++ {
			wa.iov = append(wa.iov, cctx.respQueue[i].data)
			cctx.respQueue[i].data = nil
		}

		cctx.respQueue = cctx.respQueue[head:]
		cctx.respBase += head
	} else {
		owned := make([]byte, len(data))
		copy(owned, data)
		cctx.respQueue[relIdx].ready = true
		cctx.respQueue[relIdx].data = owned
	}

	if cap(cctx.respQueue) > 256 && len(cctx.respQueue) < cap(cctx.respQueue)/4 {
		fresh := make([]responseSlot, len(cctx.respQueue), 64)
		copy(fresh, cctx.respQueue)
		cctx.respQueue = fresh
	}
}

func (h *ProxyHandler) resetClientState(c gnet.Conn, cctx *clientCtx) {
	cctx.state = stateNormal
	cctx.txQueue = cctx.txQueue[:0]
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
	if cctx.subBackend != nil {
		_ = c.EventLoop().Close(cctx.subBackend)
		cctx.subBackend = nil
	}
	if cctx.blockingBackend != nil {
		_ = c.EventLoop().Close(cctx.blockingBackend)
		cctx.blockingBackend = nil
	}
	cctx.pendingCmds = nil
}

func (h *ProxyHandler) dialDedicatedBackend(clientConn gnet.Conn, cctx *clientCtx, addr string, isSubConn bool, req *proto.Request) {
	sendErr := func(msg string) {
		if !isSubConn {
			h.fillResponseSlotInLoop(clientConn, cctx, cctx.pendingWatchSlot, proto.EncodeError(msg))
		} else {
			writeErrToClient(clientConn, msg)
		}
	}

	tcpAddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		sendErr(fmt.Sprintf("ERR failed to resolve %s: %v", addr, err))
		return
	}

	bctx := &backendCtx{
		clientConn:  clientConn,
		addr:        addr,
		isDedicated: true,
		isSubConn:   isSubConn,
	}

	ctx := gnet.NewContext(context.Background(), bctx)
	resCh, err := clientConn.EventLoop().Register(ctx, tcpAddr)
	if err != nil {
		sendErr(fmt.Sprintf("ERR failed to register backend %s: %v", addr, err))
		return
	}

	_ = h.goPool.Submit(func() {
		res := <-resCh
		if res.Err != nil {
			if !isSubConn {
				_ = clientConn.EventLoop().Execute(context.Background(), gnet.RunnableFunc(func(_ context.Context) error {
					cc, ok := clientConn.Context().(*clientCtx)
					if ok {
						h.fillResponseSlotInLoop(clientConn, cc, cc.pendingWatchSlot, proto.EncodeError(fmt.Sprintf("ERR backend connection failed: %v", res.Err)))
					}
					return nil
				}))
			} else {
				_ = clientConn.AsyncWrite(proto.EncodeError(fmt.Sprintf("ERR backend connection failed: %v", res.Err)), nil)
			}
		}
	})
}

func (h *ProxyHandler) authDedicatedConn(c gnet.Conn, cctx *clientCtx) error {
	if cctx.cfg.Password == "" {
		return nil
	}
	bctx := c.Context().(*backendCtx)
	clientConn := bctx.clientConn
	var authCmd []byte
	if cctx.cfg.Username != "" {
		authCmd = proto.EncodeCommandBytes([]byte("AUTH"), []byte(cctx.cfg.Username), []byte(cctx.cfg.Password))
	} else {
		authCmd = proto.EncodeCommandBytes([]byte("AUTH"), []byte(cctx.cfg.Password))
	}
	bctx.pending = append(bctx.pending, backendPending{
		clientConn: clientConn,
		respond: func(v *proto.Value) {
			if v.IsError() {
				cc, ok := clientConn.Context().(*clientCtx)
				if ok && !bctx.isSubConn {
					h.fillResponseSlotInLoop(clientConn, cc, cc.pendingWatchSlot, proto.EncodeError("ERR backend auth failed: "+v.Error()))
				} else {
					writeErrToClient(clientConn, "ERR backend auth failed: "+v.Error())
				}
			}
		},
	})
	_, _ = c.Write(authCmd)
	return nil
}

func writeValueToClient(c gnet.Conn, v *proto.Value) {
	_ = c.AsyncWrite(proto.EncodeValue(v), nil)
}

func writeErrToClient(c gnet.Conn, msg string) {
	_ = c.AsyncWrite(proto.EncodeError(msg), nil)
}
