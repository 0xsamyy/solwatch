package health

import (
	"context"
	"time"

	"github.com/0xsamyy/solwatch/internal/tracker"
)

// WalletLister is the minimal interface we need from the store.
type WalletLister interface {
	ListWallets(ctx context.Context) ([]string, error)
}

// Health exposes a read-only snapshot of service state for the /health command.
type Health struct {
	tm  *tracker.Manager
	st  WalletLister

	// Future: counters/metrics (e.g., reconnects, errors) can be injected here.
}

// New returns a Health aggregator bound to the tracker manager and store.
func New(tm *tracker.Manager, st WalletLister) *Health {
	return &Health{tm: tm, st: st}
}

// Report is the struct returned to the caller (Telegram handler) for formatting.
type Report struct {
	GeneratedAt time.Time `json:"generated_at"`

	// From tracker.Manager.Stats()
	Tracked int      `json:"tracked_in_memory"`
	Open    int      `json:"open_subscriptions"`
	Dropped []string `json:"dropped_subscriptions"`

	// From persistent store
	TrackedPersisted int `json:"tracked_in_store"`

	// Future: add counters like Reconnects, Errors, etc.
}

// Snapshot gathers a point-in-time report. It does not block for long operations.
func (h *Health) Snapshot(ctx context.Context) Report {
	tracked, open, dropped := h.tm.Stats()

	var persistedCount int
	if h.st != nil {
		if addrs, err := h.st.ListWallets(ctx); err == nil {
			persistedCount = len(addrs)
		}
	}

	return Report{
		GeneratedAt:      time.Now().UTC(),
		Tracked:          tracked,
		Open:             open,
		Dropped:          append([]string(nil), dropped...), // defensive copy
		TrackedPersisted: persistedCount,
	}
}
