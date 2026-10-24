package sftpfs

import (
	"context"
	"sync"

	"github.com/abunjevac/bfiles/internal/sshcfg"
)

// Pool shares one connection per destination between panels and jobs, closing it
// once the last user releases it.
type Pool struct {
	opts Options

	mu    sync.Mutex
	conns map[string]*pooled
}

type pooled struct {
	fs   *FS
	refs int
}

// NewPool creates an empty pool.
func NewPool(o Options) *Pool {
	return &Pool{opts: o, conns: map[string]*pooled{}}
}

// Get returns a connection to t, dialing if needed. Every successful Get must be
// paired with a Release.
func (p *Pool) Get(ctx context.Context, t sshcfg.Target, pr Prompter) (*FS, error) {
	key := t.Key()

	p.mu.Lock()

	if c, ok := p.conns[key]; ok {
		c.refs++
		p.mu.Unlock()

		return c.fs, nil
	}

	p.mu.Unlock()

	// dial without holding the lock: it may wait on user prompts
	f, err := Dial(ctx, t, pr, p.opts)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if c, ok := p.conns[key]; ok {
		// lost a race with another dial; keep the first connection
		_ = f.Close()
		c.refs++

		return c.fs, nil
	}

	p.conns[key] = &pooled{fs: f, refs: 1}

	go p.watch(key, f)

	return f, nil
}

// Retain adds a reference to a connection already obtained from the pool.
func (p *Pool) Retain(f *FS) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if c, ok := p.conns[f.target.Key()]; ok && c.fs == f {
		c.refs++
	}
}

// Release drops a reference and closes the connection when unused.
func (p *Pool) Release(f *FS) {
	p.mu.Lock()
	defer p.mu.Unlock()

	key := f.target.Key()

	c, ok := p.conns[key]
	if !ok || c.fs != f {
		return
	}

	c.refs--

	if c.refs <= 0 {
		delete(p.conns, key)

		_ = f.Close()
	}
}

// CloseAll closes every pooled connection.
func (p *Pool) CloseAll() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for key, c := range p.conns {
		_ = c.fs.Close()

		delete(p.conns, key)
	}
}

// watch forgets a connection once it drops, so the next Get redials.
func (p *Pool) watch(key string, f *FS) {
	_ = f.Wait()

	p.mu.Lock()
	defer p.mu.Unlock()

	if c, ok := p.conns[key]; ok && c.fs == f {
		delete(p.conns, key)
	}
}
