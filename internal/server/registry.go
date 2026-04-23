package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/butialabs/proxywi/internal/tunnel"
	"github.com/hashicorp/yamux"
)

type Agent struct {
	ID        int64
	Name      string
	RemoteIP  string
	ConnectAt time.Time

	session *yamux.Session

	bytesIn     atomic.Int64
	bytesOut    atomic.Int64
	activeConns atomic.Int64
}

func (a *Agent) BytesIn() int64     { return a.bytesIn.Load() }
func (a *Agent) BytesOut() int64    { return a.bytesOut.Load() }
func (a *Agent) ActiveConns() int64 { return a.activeConns.Load() }

func (a *Agent) OpenProxyStream(ctx context.Context, target string) (net.Conn, error) {
	stream, err := a.session.Open()
	if err != nil {
		return nil, fmt.Errorf("open stream: %w", err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = stream.SetDeadline(dl)
	}
	if err := tunnel.WriteJSONLine(stream, tunnel.ProxyRequest{Target: target}); err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("write request: %w", err)
	}
	_ = stream.SetDeadline(time.Time{})
	a.activeConns.Add(1)
	return &countingConn{Conn: stream, agent: a}, nil
}

type countingConn struct {
	net.Conn
	agent  *Agent
	closed atomic.Bool
}

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.agent.bytesIn.Add(int64(n))
	}
	return n, err
}

func (c *countingConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.agent.bytesOut.Add(int64(n))
	}
	return n, err
}

func (c *countingConn) Close() error {
	if c.closed.CompareAndSwap(false, true) {
		c.agent.activeConns.Add(-1)
	}
	return c.Conn.Close()
}

type Registry struct {
	mu           sync.RWMutex
	agents       map[int64]*Agent
	lastPickedID int64
}

func NewRegistry() *Registry {
	return &Registry{agents: map[int64]*Agent{}}
}

func (r *Registry) Add(a *Agent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if prev, ok := r.agents[a.ID]; ok {
		_ = prev.session.Close()
	}
	r.agents[a.ID] = a
}

func (r *Registry) Remove(id int64, a *Agent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur, ok := r.agents[id]; ok && cur == a {
		delete(r.agents, id)
	}
}

func (r *Registry) Get(id int64) (*Agent, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.agents[id]
	return a, ok
}

func (r *Registry) Disconnect(id int64) bool {
	r.mu.RLock()
	a, ok := r.agents[id]
	r.mu.RUnlock()
	if !ok {
		return false
	}
	_ = a.session.Close()
	return true
}

func (r *Registry) Online() []*Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Agent, 0, len(r.agents))
	for _, a := range r.agents {
		out = append(out, a)
	}
	return out
}

func (r *Registry) PickNext() (*Agent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.agents) == 0 {
		return nil, ErrNoAgents
	}
	ids := make([]int64, 0, len(r.agents))
	for id := range r.agents {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	next := ids[0]
	for _, id := range ids {
		if id > r.lastPickedID {
			next = id
			break
		}
	}
	r.lastPickedID = next
	return r.agents[next], nil
}

var ErrNoAgents = errors.New("no agents available")

