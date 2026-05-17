package engine

import (
	gnet "github.com/panjf2000/gnet/v2"
)

// NOT thread-safe: must only be accessed from the owning event loop's goroutine.
const blockingPoolCap = 10

type blockingConnPool struct {
	idle   map[string][]gnet.Conn
	reuses int
}

func newBlockingConnPool() *blockingConnPool {
	return &blockingConnPool{
		idle: make(map[string][]gnet.Conn),
	}
}

func (p *blockingConnPool) get(addr string) gnet.Conn {
	conns := p.idle[addr]
	if len(conns) == 0 {
		return nil
	}
	last := len(conns) - 1
	c := conns[last]
	p.idle[addr] = conns[:last]
	p.reuses++
	return c
}

func (p *blockingConnPool) put(addr string, c gnet.Conn) bool {
	conns := p.idle[addr]
	if len(conns) >= blockingPoolCap {
		return false
	}
	p.idle[addr] = append(conns, c)
	return true
}

func (p *blockingConnPool) size() int {
	n := 0
	for _, conns := range p.idle {
		n += len(conns)
	}
	return n
}
