package tracker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/0xsamyy/solwatch/internal/util"
)

// ActivityNotify is a package-level callback that, if set, will be called
// whenever a subscription receives an update. The string should be a fully
// formatted HTML message for Telegram (one line).
//
// Set this from the Telegram handler or the manager on initialization:
//
//   tracker.ActivityNotify = func(text string) { /* send via Telegram */ }
//
var ActivityNotify func(text string)

// Subscriber maintains a single accountSubscribe connection for one wallet.
type Subscriber struct {
	wss        string // Helius (or Solana RPC) WebSocket URL
	addr       string // wallet public key (base58, validated upstream)
	commitment string // processed|confirmed|finalized

	// state flags
	open       atomic.Bool // true when the websocket is open
	shouldOpen atomic.Bool // desired state (false after Stop)

	// internals
	stopOnce sync.Once
	stopCh   chan struct{}
}

// NewSubscriber creates a new Subscriber. It does not start it; call Run().
func NewSubscriber(wss, commitment, addr string) *Subscriber {
	s := &Subscriber{
		wss:        strings.TrimSpace(wss),
		addr:       strings.TrimSpace(addr),
		commitment: strings.TrimSpace(commitment),
		stopCh:     make(chan struct{}),
	}
	s.shouldOpen.Store(true)
	return s
}

func (s *Subscriber) IsOpen() bool       { return s.open.Load() }
func (s *Subscriber) ShouldBeOpen() bool { return s.shouldOpen.Load() }

// Stop signals the subscriber to cease reconnecting and close gracefully.
func (s *Subscriber) Stop() {
	s.stopOnce.Do(func() {
		s.shouldOpen.Store(false)
		close(s.stopCh)
	})
}

// Run is a long-running method: it connects, subscribes, reads updates,
// and auto-reconnects with exponential backoff + jitter until Stop() or ctx cancel.
func (s *Subscriber) Run(ctx context.Context) {
	bo := util.NewBackoff(1*time.Second, 30*time.Second, 2.0, 0.2)

	for {
		// Exit conditions
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		default:
		}

		// Dial
		dialer := websocket.Dialer{
			Proxy:            http.ProxyFromEnvironment,
			HandshakeTimeout: 12 * time.Second,
			EnableCompression: true,
		}
		conn, _, err := dialer.DialContext(ctx, s.wss, nil)
		if err != nil {
			wait := bo.Next()
			log.Printf("[sub %s] dial error: %v; retry in %s", s.shortAddr(), err, wait)
			select {
			case <-ctx.Done():
				return
			case <-s.stopCh:
				return
			case <-time.After(wait):
				continue
			}
		}
		// Connected
		s.open.Store(true)
		bo.Reset()

		// Ensure proper close on this iteration
		closed := make(chan struct{})
		go func() {
			<-s.stopCh
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "stop"), time.Now().Add(2*time.Second))
			_ = conn.Close()
			close(closed)
		}()

		// Keep read deadlines fresh via pong handler
		_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		conn.SetPongHandler(func(string) error {
			return conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		})

		// Subscribe
		subMsg := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "accountSubscribe",
			"params": []any{
				s.addr,
				map[string]any{
					"encoding":   "jsonParsed",
					"commitment": s.commitment,
				},
			},
		}
		if err := conn.WriteJSON(subMsg); err != nil {
			log.Printf("[sub %s] write subscribe error: %v", s.shortAddr(), err)
			s.open.Store(false)
			_ = conn.Close()
			wait := bo.Next()
			select {
			case <-ctx.Done():
				return
			case <-s.stopCh:
				return
			case <-time.After(wait):
				continue
			}
		}

		// Ping loop to keep the connection alive (every 20s)
		pingStop := make(chan struct{})
		go func() {
			t := time.NewTicker(20 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-pingStop:
					return
				case <-s.stopCh:
					return
				case <-ctx.Done():
					return
				case <-t.C:
					_ = conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(5*time.Second))
				}
			}
		}()

		// Read loop: any push update => "activity detected"
		readErr := func() error {
			for {
				// Read
				_, msg, err := conn.ReadMessage()
				if err != nil {
					return err
				}
				// Parse minimal JSON to distinguish sub ack vs. update
				if isNotif(msg) {
					// produce one-line HTML with short link
					short := s.shortAddr()
					link := fmt.Sprintf(`activity detected: <a href="https://solscan.io/account/%s">%s</a>`, s.addr, short)
					if ActivityNotify != nil {
						ActivityNotify(link)
					}
				}
			}
		}()

		// Tear down and backoff
		close(pingStop)
		s.open.Store(false)
		_ = conn.Close()

		if readErr != nil {
			wait := bo.Next()
			log.Printf("[sub %s] read error: %v; reconnect in %s", s.shortAddr(), readErr, wait)
			select {
			case <-ctx.Done():
				return
			case <-s.stopCh:
				return
			case <-time.After(wait):
				continue
			}
		}

		// Also wait for stop-close (if Stop() was invoked during connection)
		select {
		case <-closed:
			return
		default:
		}
	}
}

// isNotif determines if a raw JSON message is a subscription notification.
// For Solana JSON-RPC, subscription pushes generally carry a "method" field
// like "accountNotification" (or vendor variant). Initial subscribe success
// has "result" with a subscription id.
//
// Heuristic: treat messages with "method" AND "params" as updates;
// messages with top-level "result" are subscribe ACKs.
func isNotif(raw []byte) bool {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return false
	}
	if _, ok := m["result"]; ok {
		// subscribe ACK or other call result
		return false
	}
	if meth, ok := m["method"].(string); ok {
		if strings.Contains(strings.ToLower(meth), "account") {
			if _, ok := m["params"]; ok {
				return true
			}
		}
	}
	// Some providers omit "method" for pushes; fallback: if "params" exists with "result", treat as update.
	if params, ok := m["params"].(map[string]any); ok {
		if _, has := params["result"]; has {
			return true
		}
	}
	return false
}

func (s *Subscriber) shortAddr() string {
	if len(s.addr) < 4 {
		return s.addr
	}
	return s.addr[:4] + "..."
}
