package util

import (
	"math/rand"
	"sync"
	"time"
)

// Backoff implements exponential backoff with jitter.
//
// Typical usage:
//
//   b := util.NewBackoff(1*time.Second, 30*time.Second, 2.0, 0.2)
//   for attempt := 0; attempt < 10; attempt++ {
//       wait := b.Next()
//       log.Printf("retrying in %s", wait)
//       time.Sleep(wait)
//       if doSomething() {
//           b.Reset()
//           break
//       }
//   }
//
// This avoids hammering a service after errors, while spreading retries
// randomly to avoid synchronized reconnect storms.
type Backoff struct {
	mu      sync.Mutex
	min     time.Duration
	max     time.Duration
	factor  float64 // growth multiplier, e.g. 2.0
	jitter  float64 // percentage of randomness, e.g. 0.2 = ±20%
	attempt int
}

// NewBackoff creates a new Backoff.
// - min: initial duration (e.g. 1s)
// - max: maximum cap (e.g. 30s)
// - factor: exponential multiplier (e.g. 2.0)
// - jitter: percentage of randomness [0.0–1.0]; e.g. 0.2 = ±20%
func NewBackoff(min, max time.Duration, factor, jitter float64) *Backoff {
	if min <= 0 {
		min = time.Second
	}
	if max < min {
		max = min
	}
	if factor < 1.1 {
		factor = 2.0
	}
	if jitter < 0 {
		jitter = 0
	}
	if jitter > 1 {
		jitter = 1
	}
	// Seed once at package init, so all Backoff instances share a PRNG
	rand.Seed(time.Now().UnixNano())

	return &Backoff{
		min:    min,
		max:    max,
		factor: factor,
		jitter: jitter,
	}
}

// Next returns the next backoff duration with jitter applied.
func (b *Backoff) Next() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()

	// calculate exponential step
	backoff := float64(b.min) * pow(b.factor, float64(b.attempt))
	if backoff > float64(b.max) {
		backoff = float64(b.max)
	}
	b.attempt++

	// apply jitter: multiply by (1 ± jitter*rand)
	if b.jitter > 0 {
		j := (rand.Float64()*2 - 1) * b.jitter // [-jitter, +jitter]
		backoff = backoff * (1 + j)
	}

	if backoff < float64(b.min) {
		backoff = float64(b.min)
	}

	return time.Duration(backoff)
}

// Reset clears the attempt counter, so the next backoff is min.
func (b *Backoff) Reset() {
	b.mu.Lock()
	b.attempt = 0
	b.mu.Unlock()
}

// pow is a tiny inline float power helper (faster than math.Pow for ints).
func pow(base, exp float64) float64 {
	result := 1.0
	for i := 0; i < int(exp); i++ {
		result *= base
	}
	return result
}
