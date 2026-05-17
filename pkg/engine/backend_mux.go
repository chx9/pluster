package engine

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"

	gnet "github.com/panjf2000/gnet/v2"

	"github.com/pluster/pluster/pkg/cluster"
	"github.com/pluster/pluster/pkg/proto"
)

type pendingClientReq struct {
	clientConn    gnet.Conn
	slotIdx       int
	depth         int
	req           *proto.Request
	skipReply     bool
	isAskSentinel bool
	fanIn         *fanInState
	fanInIdx      int
}

type backendMuxConn struct {
	conn     gnet.Conn
	addr     string
	pending  []pendingClientReq
	head     int
	partial  []byte
	writeBuf []byte
}

type connWriteAccum struct {
	iov [][]byte
}

var connWriteAccumPool = sync.Pool{New: func() any { return &connWriteAccum{iov: make([][]byte, 0, 32)} }}

type backendMux struct {
	conns         map[string]*backendMuxConn
	el            gnet.EventLoop
	handler       *ProxyHandler
	dialing       map[string][]pendingClientReq
	writeAccum    map[gnet.Conn]*connWriteAccum
	rawBuf        []byte
	localCmdCount uint64
}

func newBackendMux(el gnet.EventLoop, h *ProxyHandler) *backendMux {
	return &backendMux{
		conns:      make(map[string]*backendMuxConn),
		dialing:    make(map[string][]pendingClientReq),
		writeAccum: make(map[gnet.Conn]*connWriteAccum),
		rawBuf:     make([]byte, 0, 4096),
		el:         el,
		handler:    h,
	}
}

func (m *backendMux) send(addr string, req pendingClientReq) {
	if bc, ok := m.conns[addr]; ok {
		bc.pending = append(bc.pending, req)
		bc.writeBuf = append(bc.writeBuf, rawBytes(req.req)...)
		return
	}

	m.dialing[addr] = append(m.dialing[addr], req)
	if len(m.dialing[addr]) == 1 {
		m.dialBackend(addr)
	}
}

func (m *backendMux) flushBackendWrites() {
	for _, bc := range m.conns {
		if len(bc.writeBuf) > 0 {
			_, _ = bc.conn.Write(bc.writeBuf)
			bc.writeBuf = bc.writeBuf[:0]
		}
	}
}

func rawBytes(req *proto.Request) []byte {
	if len(req.Raw) > 0 {
		return req.Raw
	}
	return proto.EncodeCommandBytes(req.Args...)
}

func (m *backendMux) dialBackend(addr string) {
	tcpAddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		m.failDialing(addr, fmt.Errorf("resolve %s: %w", addr, err))
		return
	}

	bctx := &backendCtx{
		addr:        addr,
		isDedicated: false,
	}
	ctx := gnet.NewContext(context.Background(), bctx)
	resCh, err := m.el.Register(ctx, tcpAddr)
	if err != nil {
		m.failDialing(addr, fmt.Errorf("register %s: %w", addr, err))
		return
	}

	go func() {
		res := <-resCh
		if res.Err != nil {
			_ = m.el.Execute(context.Background(), gnet.RunnableFunc(func(_ context.Context) error {
				m.failDialing(addr, res.Err)
				return nil
			}))
		}
	}()
}

var readonlyBytes = []byte("*1\r\n$8\r\nREADONLY\r\n")

func (m *backendMux) isReplicaAddr(addr string) bool {
	topo := m.handler.topoMgr.LoadTopo()
	if topo == nil {
		return false
	}
	node := topo.GetNodeByAddr(addr)
	return node != nil && node.Role == cluster.RoleReplica
}

func (m *backendMux) onOpen(c gnet.Conn, addr string) {
	bc := &backendMuxConn{
		conn:     c,
		addr:     addr,
		pending:  make([]pendingClientReq, 0, 64),
		writeBuf: make([]byte, 0, 4096),
	}
	m.conns[addr] = bc
	m.handler.incMuxConn(addr)

	queued := m.dialing[addr]
	delete(m.dialing, addr)

	if m.isReplicaAddr(addr) && len(queued) > 0 {
		bc.pending = append(bc.pending, pendingClientReq{
			clientConn: queued[0].clientConn,
			slotIdx:    queued[0].slotIdx,
			skipReply:  true,
		})
		_, _ = c.Write(readonlyBytes)
	}

	for _, req := range queued {
		bc.pending = append(bc.pending, req)
		if req.isAskSentinel {
			_, _ = c.Write(askingBytes)
		} else {
			_, _ = c.Write(rawBytes(req.req))
		}
	}
}

func (m *backendMux) onClose(addr string) {
	if bc, ok := m.conns[addr]; ok {
		errMsg := fmt.Sprintf("ERR cluster node disconnected: %s", addr)
		for i := bc.head; i < len(bc.pending); i++ {
			req := bc.pending[i]
			if req.skipReply {
				continue
			}
			cctx, ok := req.clientConn.Context().(*clientCtx)
			if ok {
				m.handler.fillResponseSlot(req.clientConn, cctx, req.slotIdx, proto.EncodeError(errMsg))
			}
		}
		delete(m.conns, addr)
		m.handler.decMuxConn(addr)
	}
}

const maxMuxRedirects = 16

func (m *backendMux) onTraffic(c gnet.Conn, bctx *backendCtx) gnet.Action {
	bc := m.conns[bctx.addr]
	if bc == nil {
		return gnet.Close
	}

	buf, _ := c.Peek(-1)
	if len(buf) == 0 {
		return gnet.None
	}

	totalConsumed := 0
	rawBuf := m.rawBuf[:0]

	for {
		remaining := buf[totalConsumed:]
		if len(remaining) == 0 {
			break
		}

		consumed, kind, errMsg := proto.ScanValue(remaining)
		if consumed == 0 {
			break
		}

		rawStart := len(rawBuf)
		rawBuf = append(rawBuf, remaining[:consumed]...)
		raw := rawBuf[rawStart : rawStart+consumed]
		totalConsumed += consumed

		if bc.head >= len(bc.pending) {
			continue
		}
		req := bc.pending[bc.head]
		bc.head++
		if bc.head == len(bc.pending) {
			bc.head = 0
			bc.pending = bc.pending[:0]
		}

		if req.skipReply {
			continue
		}

		if kind == proto.ErrorKindMoved && req.depth < maxMuxRedirects {
			_, newAddr, parseErr := parseRedirectionMsg(errMsg)
			if parseErr == nil {
				m.handler.topoMgr.TriggerRefresh()
				m.send(newAddr, pendingClientReq{
					clientConn: req.clientConn,
					slotIdx:    req.slotIdx,
					depth:      req.depth + 1,
					req:        req.req,
					fanIn:      req.fanIn,
					fanInIdx:   req.fanInIdx,
				})
				continue
			}
		}

		if kind == proto.ErrorKindAsk && req.depth < maxMuxRedirects {
			_, newAddr, parseErr := parseRedirectionMsg(errMsg)
			if parseErr == nil {
				m.sendAsk(newAddr, pendingClientReq{
					clientConn: req.clientConn,
					slotIdx:    req.slotIdx,
					depth:      req.depth + 1,
					req:        req.req,
					fanIn:      req.fanIn,
					fanInIdx:   req.fanInIdx,
				})
				continue
			}
		}

		cctx, ok := req.clientConn.Context().(*clientCtx)
		if !ok {
			continue
		}
		if req.fanIn != nil {
			m.handler.accumulateFanInSubResponse(req.clientConn, cctx, req.slotIdx, req.fanInIdx, raw, m.writeAccum)
		} else {
			m.handler.accumulateResponseSlot(req.clientConn, cctx, req.slotIdx, raw, m.writeAccum)
		}
	}

	if totalConsumed > 0 {
		_, _ = c.Discard(totalConsumed)
	}

	m.flushWriteAccum()
	m.flushBackendWrites()

	if totalConsumed > 0 {
		if len(rawBuf) > 4096 {
			m.rawBuf = nil
		} else {
			m.rawBuf = rawBuf[:0]
		}
	}
	return gnet.None
}

func (m *backendMux) flushWriteAccum() {
	for conn, accum := range m.writeAccum {
		if len(accum.iov) == 1 {
			_, _ = conn.Write(accum.iov[0])
		} else if len(accum.iov) > 1 {
			_, _ = conn.Writev(accum.iov)
		}
		accum.iov = accum.iov[:0]
		connWriteAccumPool.Put(accum)
		delete(m.writeAccum, conn)
	}
}

var askingBytes = []byte("*1\r\n$6\r\nASKING\r\n")

func (m *backendMux) sendAsk(addr string, req pendingClientReq) {
	skip := pendingClientReq{
		clientConn:    req.clientConn,
		slotIdx:       req.slotIdx,
		depth:         req.depth,
		req:           req.req,
		skipReply:     true,
		isAskSentinel: true,
	}
	if bc, ok := m.conns[addr]; ok {
		bc.pending = append(bc.pending, skip, req)
		_, _ = bc.conn.Write(askingBytes)
		_, _ = bc.conn.Write(rawBytes(req.req))
		return
	}

	wasEmpty := len(m.dialing[addr]) == 0
	m.dialing[addr] = append(m.dialing[addr], skip, req)
	if wasEmpty {
		m.dialBackend(addr)
	}
}

func (m *backendMux) connCountByAddr() map[string]int {
	counts := make(map[string]int, len(m.conns))
	for addr := range m.conns {
		counts[addr]++
	}
	return counts
}

func (m *backendMux) failDialing(addr string, err error) {
	queued := m.dialing[addr]
	delete(m.dialing, addr)
	for _, req := range queued {
		if req.skipReply {
			continue
		}
		cctx, ok := req.clientConn.Context().(*clientCtx)
		if ok {
			m.handler.fillResponseSlot(req.clientConn, cctx, req.slotIdx, proto.EncodeError(err.Error()))
		}
	}
}

func parseRedirectionMsg(msg []byte) (slot int, addr string, err error) {
	parts := bytes.SplitN(msg, []byte(" "), 3)
	if len(parts) != 3 {
		return 0, "", fmt.Errorf("invalid redirection: %s", msg)
	}
	s, e := strconv.Atoi(string(parts[1]))
	if e != nil {
		return 0, "", fmt.Errorf("invalid slot: %s", msg)
	}
	return s, string(parts[2]), nil
}
