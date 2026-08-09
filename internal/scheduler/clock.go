package scheduler

import (
	"sync"
	"time"
)

// Clock abstracts "now" so Engine's tick loop can be driven by a real
// ticker in production and by an instantly-advanceable fake in tests —
// NFR-TEST-2's simulated-week scenarios need to cover days of pacing
// without a test actually taking days.
type Clock interface {
	Now() time.Time
}

// RealClock is the production Clock, backed by time.Now.
type RealClock struct{}

// Now returns the current wall-clock time.
func (RealClock) Now() time.Time { return time.Now() }

// SimClock is a manually-advanced Clock for tests.
type SimClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewSimClock returns a SimClock starting at start.
func NewSimClock(start time.Time) *SimClock {
	return &SimClock{now: start}
}

// Now returns the clock's current simulated time.
func (c *SimClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the simulated clock forward by d.
func (c *SimClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Set moves the simulated clock to an absolute time. Useful for jumping
// straight to a window's resets_at rather than advancing in small steps.
func (c *SimClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}
