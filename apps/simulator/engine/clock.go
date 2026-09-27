package engine

// Clock tracks simulated time. It is owned by the runner and only advanced
// when an event is popped from the queue — never by wall-clock sleeps.
type Clock struct {
	now    Time
	paused bool
}

// NewClock returns a clock at the simulation's zero instant.
func NewClock() *Clock { return &Clock{} }

// Now returns the current simulated instant.
func (c *Clock) Now() Time { return c.now }

// Set is used internally by the runner to jump to an event's timestamp.
func (c *Clock) Set(t Time) { c.now = t }

// Paused reports whether the clock is currently suspended.
func (c *Clock) Paused() bool { return c.paused }

// Pause suspends the clock. The runner idles until Resume, without
// advancing simulated time and without scheduling anything.
func (c *Clock) Pause() { c.paused = true }

// Resume lifts a pause; a nil-safe no-op when not paused.
func (c *Clock) Resume() { c.paused = false }
