package tracker

import (
	"context"
	"sort"
	"sync"
)

// Manager owns the set of active Subscribers (one per wallet).
// It is concurrency-safe via an internal RWMutex.
type Manager struct {
	wss        string
	commitment string

	mu   sync.RWMutex
	subs map[string]*Subscriber // addr -> sub
}

// NewManager constructs a Manager that will spawn subscribers using the
// provided WebSocket endpoint and commitment level.
func NewManager(wss, commitment string) *Manager {
	return &Manager{
		wss:        wss,
		commitment: commitment,
		subs:       make(map[string]*Subscriber),
	}
}

// Track ensures there is a running subscriber for addr.
// If one already exists, this is a no-op.
func (m *Manager) Track(ctx context.Context, addr string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.subs[addr]; exists {
		return nil
	}

	sub := NewSubscriber(m.wss, m.commitment, addr)
	m.subs[addr] = sub
	go sub.Run(ctx) // long-running; will auto-reconnect until Stop or ctx cancel
	return nil
}

// Untrack stops and removes the subscriber for addr, if present.
func (m *Manager) Untrack(_ context.Context, addr string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if sub, ok := m.subs[addr]; ok {
		sub.Stop()        // graceful: closes WS and halts reconnect attempts
		delete(m.subs, addr)
	}
	return nil
}

// List returns a sorted snapshot of currently tracked addresses.
func (m *Manager) List() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]string, 0, len(m.subs))
	for addr := range m.subs {
		out = append(out, addr)
	}
	sort.Strings(out)
	return out
}

// Stats reports:
//   tracked = total number of subscribers in memory
//   open    = how many currently report IsOpen()==true
//   dropped = addresses that ShouldBeOpen()==true but IsOpen()==false
//
// This is used by the /health command.
func (m *Manager) Stats() (tracked int, open int, dropped []string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tracked = len(m.subs)
	for addr, s := range m.subs {
		if s.IsOpen() {
			open++
			continue
		}
		if s.ShouldBeOpen() {
			dropped = append(dropped, addr)
		}
	}
	// Keep output deterministic for tests / logs.
	sort.Strings(dropped)
	return
}

// StopAll is a helper to gracefully stop every subscriber.
// (Not required for your commands, but useful for clean shutdowns.)
func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for addr, s := range m.subs {
		_ = addr // for symmetry; not used
		s.Stop()
	}
}
