package pool

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pluster/pluster/pkg/proto"
)

var ErrPoolClosed = errors.New("connection pool is closed")

const (
	DefaultPoolSize    = 10
	DefaultIdleTimeout = 30 * time.Second
	DefaultDialTimeout = 3 * time.Second
)

// Conn wraps a net.Conn with pooling metadata and pre-allocated RESP
// Reader/Writer so that bufio buffers are reused across requests rather
// than allocated fresh on every call.
type Conn struct {
	net.Conn
	Reader    *proto.Reader
	Writer    *proto.Writer
	pool      *Pool
	createdAt time.Time
	usedAt    time.Time
	broken    bool
}

func (c *Conn) Close() error {
	if c.pool != nil {
		return c.pool.put(c)
	}
	return c.Conn.Close()
}

func (c *Conn) MarkBroken() {
	c.broken = true
}

type Pool struct {
	addr        string
	username    string
	password    string
	maxSize     int
	idleTimeout time.Duration
	dialTimeout time.Duration
	readonly    bool

	mu       sync.Mutex
	conns    []*Conn
	numConns int
	closed   atomic.Bool
	waiters  chan struct{}
}

type Options struct {
	Addr        string
	Username    string
	Password    string
	MaxSize     int
	IdleTimeout time.Duration
	DialTimeout time.Duration
	Readonly    bool
}

func New(opts Options) *Pool {
	if opts.MaxSize <= 0 {
		opts.MaxSize = DefaultPoolSize
	}
	if opts.IdleTimeout <= 0 {
		opts.IdleTimeout = DefaultIdleTimeout
	}
	if opts.DialTimeout <= 0 {
		opts.DialTimeout = DefaultDialTimeout
	}
	return &Pool{
		addr:        opts.Addr,
		username:    opts.Username,
		password:    opts.Password,
		maxSize:     opts.MaxSize,
		idleTimeout: opts.IdleTimeout,
		dialTimeout: opts.DialTimeout,
		readonly:    opts.Readonly,
		waiters:     make(chan struct{}, opts.MaxSize),
	}
}

func (p *Pool) Get(ctx context.Context) (*Conn, error) {
	if p.closed.Load() {
		return nil, ErrPoolClosed
	}

	for {
		p.mu.Lock()
		if c := p.popIdle(); c != nil {
			p.mu.Unlock()
			return c, nil
		}
		if p.numConns < p.maxSize {
			p.numConns++
			p.mu.Unlock()
			c, err := p.dial(ctx)
			if err != nil {
				p.mu.Lock()
				p.numConns--
				p.mu.Unlock()
				p.notify()
				return nil, err
			}
			return c, nil
		}
		p.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-p.waiters:
		}
	}
}

func (p *Pool) popIdle() *Conn {
	for len(p.conns) > 0 {
		c := p.conns[len(p.conns)-1]
		p.conns = p.conns[:len(p.conns)-1]
		if p.idleTimeout > 0 && time.Since(c.usedAt) > p.idleTimeout {
			_ = c.Conn.Close()
			p.numConns--
			continue
		}
		return c
	}
	return nil
}

func (p *Pool) dial(ctx context.Context) (*Conn, error) {
	d := net.Dialer{Timeout: p.dialTimeout}
	nc, err := d.DialContext(ctx, "tcp", p.addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", p.addr, err)
	}

	w := proto.NewWriter(nc)
	r := proto.NewReader(nc)

	if p.password != "" {
		if err := p.auth(w, r); err != nil {
			_ = nc.Close()
			return nil, err
		}
	}

	if p.readonly {
		if err := p.sendReadonly(w, r); err != nil {
			_ = nc.Close()
			return nil, err
		}
	}

	c := &Conn{
		Conn:      nc,
		pool:      p,
		createdAt: time.Now(),
		usedAt:    time.Now(),
	}
	c.Reader = r
	c.Writer = w
	return c, nil
}

func (p *Pool) sendReadonly(w *proto.Writer, r *proto.Reader) error {
	if err := w.WriteCommand("READONLY"); err != nil {
		return err
	}
	if err := w.Flush(); err != nil {
		return err
	}
	v, err := r.ReadValue()
	if err != nil {
		return err
	}
	if v.IsError() {
		return errors.New("READONLY failed: " + v.Error())
	}
	return nil
}

func (p *Pool) auth(w *proto.Writer, r *proto.Reader) error {
	if p.username != "" {
		if err := w.WriteCommand("AUTH", p.username, p.password); err != nil {
			return err
		}
	} else {
		if err := w.WriteCommand("AUTH", p.password); err != nil {
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

func (p *Pool) put(c *Conn) error {
	if c.broken || p.closed.Load() {
		p.mu.Lock()
		p.numConns--
		p.mu.Unlock()
		p.notify()
		return c.Conn.Close()
	}
	p.mu.Lock()
	if len(p.conns) < p.maxSize {
		c.usedAt = time.Now()
		p.conns = append(p.conns, c)
		p.mu.Unlock()
		p.notify()
		return nil
	}
	p.numConns--
	p.mu.Unlock()
	p.notify()
	return c.Conn.Close()
}

func (p *Pool) notify() {
	select {
	case p.waiters <- struct{}{}:
	default:
	}
}

func (p *Pool) Close() {
	p.closed.Store(true)
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		_ = c.Conn.Close()
	}
	p.conns = nil
	p.numConns = 0
}

func (p *Pool) Stats() (total, idle int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.numConns, len(p.conns)
}

type Manager struct {
	reg      poolRegistry[*Pool]
	username string
	password string
	maxSize  int
}

func NewManager(username, password string, maxSize int) *Manager {
	if maxSize <= 0 {
		maxSize = DefaultPoolSize
	}
	return &Manager{
		reg:      newPoolRegistry[*Pool](),
		username: username,
		password: password,
		maxSize:  maxSize,
	}
}

func (m *Manager) GetPool(addr string) *Pool {
	return m.reg.getOrCreate(addr, m.newPool)
}

func (m *Manager) GetReadonlyPool(addr string) *Pool {
	return m.reg.getOrCreate(addr+"#ro", m.newPool)
}

func (m *Manager) newPool(addr string, readonly bool) *Pool {
	return New(Options{
		Addr:     addr,
		Username: m.username,
		Password: m.password,
		MaxSize:  m.maxSize,
		Readonly: readonly,
	})
}

func (m *Manager) Username() string { return m.username }
func (m *Manager) Password() string { return m.password }

func (m *Manager) RemovePool(addr string) {
	m.reg.remove(addr)
	m.reg.remove(addr + "#ro")
}

func (m *Manager) Close() {
	m.reg.closeAll()
}

func (m *Manager) ConnCountByAddr() map[string]int {
	m.reg.mu.RLock()
	defer m.reg.mu.RUnlock()
	counts := make(map[string]int, len(m.reg.pools))
	for key, p := range m.reg.pools {
		addr := key
		if strings.HasSuffix(key, "#ro") {
			addr = strings.TrimSuffix(key, "#ro")
		}
		total, _ := p.Stats()
		if total > 0 {
			counts[addr] += total
		}
	}
	return counts
}
