package engine

// Clock tracks simulated time. It is owned by the runner and only advanced
// when an event is popped from the queue — never by wall-clock sleeps.
type Clock struct {
	now Time
}

// NewClock returns a clock at the simulation's zero instant.
func NewClock() *Clock { return &Clock{} }

// Now returns the current simulated instant.
func (c *Clock) Now() Time { return c.now }

// Set is used internally by the runner to jump to an event's timestamp.
func (c *Clock) Set(t Time) { c.now = t }
