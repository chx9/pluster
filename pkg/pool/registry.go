package pool

import (
	"strings"
	"sync"
)

type closer interface {
	Close()
}

type poolRegistry[P closer] struct {
	mu    sync.RWMutex
	pools map[string]P
}

func newPoolRegistry[P closer]() poolRegistry[P] {
	return poolRegistry[P]{pools: make(map[string]P)}
}

func (r *poolRegistry[P]) getOrCreate(key string, factory func(addr string, readonly bool) P) P {
	addr := strings.TrimSuffix(key, "#ro")
	readonly := strings.HasSuffix(key, "#ro")

	r.mu.RLock()
	p, ok := r.pools[key]
	r.mu.RUnlock()
	if ok {
		return p
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok = r.pools[key]; ok {
		return p
	}
	p = factory(addr, readonly)
	r.pools[key] = p
	return p
}

func (r *poolRegistry[P]) closeAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.pools {
		p.Close()
	}
	r.pools = make(map[string]P)
}

func (r *poolRegistry[P]) remove(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.pools[key]; ok {
		p.Close()
		delete(r.pools, key)
	}
}
