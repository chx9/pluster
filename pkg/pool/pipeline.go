package pool

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/pluster/pluster/pkg/proto"
)

// Reconnect backoff parameters for pipeConn.
// When a backend node is down, we back off exponentially to avoid
// a flood of 3-second dial-timeout failures that would pile up in the
// request channel and cause slow error delivery to clients.
const (
	pipeConnBackoffBase = 100 * time.Millisecond
	pipeConnBackoffMax  = 4 * time.Second
)

type pipeReq struct {
	data []byte
	resp chan pipeResp
}

type pipeResp struct {
	val *proto.Value
	err error
}

type PipelinedPool struct {
	addr     string
	username string
	password string
	size     int
	readonly bool

	mu    sync.Mutex
	conns []*pipeConn
	idx   int
}

func NewPipelinedPool(addr, username, password string, size int) *PipelinedPool {
	return NewPipelinedPoolOpts(addr, username, password, size, false)
}

func NewPipelinedPoolOpts(addr, username, password string, size int, readonly bool) *PipelinedPool {
	if size <= 0 {
		size = 4
	}
	pp := &PipelinedPool{
		addr:     addr,
		username: username,
		password: password,
		size:     size,
		readonly: readonly,
	}
	pp.conns = make([]*pipeConn, size)
	for i := 0; i < size; i++ {
		pp.conns[i] = newPipeConn(addr, username, password, readonly)
	}
	return pp
}

func (pp *PipelinedPool) Do(data []byte) (*proto.Value, error) {
	pp.mu.Lock()
	c := pp.conns[pp.idx%pp.size]
	pp.idx++
	pp.mu.Unlock()
	return c.do(data)
}

func (pp *PipelinedPool) Close() {
	pp.mu.Lock()
	defer pp.mu.Unlock()
	for _, c := range pp.conns {
		c.close()
	}
}

type pipeConn struct {
	addr     string
	username string
	password string
	readonly bool
	reqCh    chan pipeReq
	done     chan struct{}

	backoffUntil time.Time
	backoffDelay time.Duration
}

func newPipeConn(addr, username, password string, readonly bool) *pipeConn {
	pc := &pipeConn{
		addr:     addr,
		username: username,
		password: password,
		readonly: readonly,
		reqCh:    make(chan pipeReq, 512),
		done:     make(chan struct{}),
	}
	go pc.loop()
	return pc
}

func (pc *pipeConn) do(data []byte) (*proto.Value, error) {
	resp := make(chan pipeResp, 1)
	pc.reqCh <- pipeReq{data: data, resp: resp}
	r := <-resp
	return r.val, r.err
}

func (pc *pipeConn) close() {
	close(pc.reqCh)
	<-pc.done
}

func (pc *pipeConn) loop() {
	defer close(pc.done)
	var nc net.Conn
	var r *proto.Reader
	var w *proto.Writer

	connect := func() error {
		if nc != nil {
			_ = nc.Close()
		}
		d := net.Dialer{Timeout: 3 * time.Second}
		c, err := d.Dial("tcp", pc.addr)
		if err != nil {
			return err
		}
		wr := proto.NewWriter(c)
		rd := proto.NewReader(c)
		if pc.password != "" {
			var authErr error
			if pc.username != "" {
				authErr = wr.WriteCommand("AUTH", pc.username, pc.password)
			} else {
				authErr = wr.WriteCommand("AUTH", pc.password)
			}
			if authErr != nil {
				_ = c.Close()
				return authErr
			}
			if err := wr.Flush(); err != nil {
				_ = c.Close()
				return err
			}
			v, err := rd.ReadValue()
			if err != nil || v.IsError() {
				_ = c.Close()
				return fmt.Errorf("auth failed")
			}
		}
		if pc.readonly {
			if err := wr.WriteCommand("READONLY"); err != nil {
				_ = c.Close()
				return err
			}
			if err := wr.Flush(); err != nil {
				_ = c.Close()
				return err
			}
			v, err := rd.ReadValue()
			if err != nil || v.IsError() {
				_ = c.Close()
				return fmt.Errorf("READONLY failed")
			}
		}
		nc = c
		r = rd
		w = wr
		return nil
	}

	pending := make([]pipeReq, 0, 64)

	for req := range pc.reqCh {
		pending = pending[:0]
		pending = append(pending, req)

	drainMore:
		for len(pending) < 64 {
			select {
			case more, ok := <-pc.reqCh:
				if !ok {
					goto shutdown
				}
				pending = append(pending, more)
			default:
				break drainMore
			}
		}

		if nc == nil {
			if wait := time.Until(pc.backoffUntil); wait > 0 {
				for _, p := range pending {
					p.resp <- pipeResp{err: fmt.Errorf("backend %s unavailable (reconnecting)", pc.addr)}
				}
				continue
			}
			if err := connect(); err != nil {
				if pc.backoffDelay == 0 {
					pc.backoffDelay = pipeConnBackoffBase
				} else {
					pc.backoffDelay *= 2
					if pc.backoffDelay > pipeConnBackoffMax {
						pc.backoffDelay = pipeConnBackoffMax
					}
				}
				pc.backoffUntil = time.Now().Add(pc.backoffDelay)
				for _, p := range pending {
					p.resp <- pipeResp{err: err}
				}
				continue
			}
			pc.backoffDelay = 0
			pc.backoffUntil = time.Time{}
		}

		writeOK := true
		for _, p := range pending {
			if _, err := w.WriteRaw(p.data); err != nil {
				writeOK = false
				break
			}
		}
		if !writeOK || w.Flush() != nil {
			_ = nc.Close()
			nc = nil
			pc.backoffDelay = pipeConnBackoffBase
			pc.backoffUntil = time.Now().Add(pc.backoffDelay)
			for _, p := range pending {
				p.resp <- pipeResp{err: fmt.Errorf("write error")}
			}
			continue
		}

		for i, p := range pending {
			v, err := r.ReadValue()
			if err != nil {
				_ = nc.Close()
				nc = nil
				pc.backoffDelay = pipeConnBackoffBase
				pc.backoffUntil = time.Now().Add(pc.backoffDelay)
				p.resp <- pipeResp{err: err}
				for _, remaining := range pending[i+1:] {
					remaining.resp <- pipeResp{err: fmt.Errorf("connection reset")}
				}
				break
			}
			p.resp <- pipeResp{val: v}
		}
	}

shutdown:
	if nc != nil {
		_ = nc.Close()
	}
}

type PipelinedManager struct {
	reg      poolRegistry[*PipelinedPool]
	username string
	password string
	size     int
}

func NewPipelinedManager(username, password string, size int) *PipelinedManager {
	if size <= 0 {
		size = 4
	}
	return &PipelinedManager{
		reg:      newPoolRegistry[*PipelinedPool](),
		username: username,
		password: password,
		size:     size,
	}
}

func (m *PipelinedManager) GetPool(addr string) *PipelinedPool {
	return m.reg.getOrCreate(addr, m.newPool)
}

func (m *PipelinedManager) GetReadonlyPool(addr string) *PipelinedPool {
	return m.reg.getOrCreate(addr+"#ro", m.newPool)
}

func (m *PipelinedManager) newPool(addr string, readonly bool) *PipelinedPool {
	return NewPipelinedPoolOpts(addr, m.username, m.password, m.size, readonly)
}

func (m *PipelinedManager) RemovePool(addr string) {
	m.reg.remove(addr)
	m.reg.remove(addr + "#ro")
}

func (m *PipelinedManager) Close() {
	m.reg.closeAll()
}

func (m *PipelinedManager) ConnCountByAddr() map[string]int {
	m.reg.mu.RLock()
	defer m.reg.mu.RUnlock()
	counts := make(map[string]int, len(m.reg.pools))
	for key, p := range m.reg.pools {
		addr := strings.TrimSuffix(key, "#ro")
		counts[addr] += p.size
	}
	return counts
}
