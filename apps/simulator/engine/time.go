package engine

import (
	"fmt"
	"strconv"
	"strings"
)

// Duration is a span of simulated time in nanoseconds.
//
// Using a distinct int64 type (instead of time.Duration) makes the engine's
// clock independent of the wall clock: simulation code cannot accidentally
// mix simulated time with real time.
type Duration int64

// Common simulated-time durations. Simulation logic should compose these
// rather than inventing wall-clock values.
const (
	Nanosecond  Duration = 1
	Microsecond Duration = 1000 * Nanosecond
	Millisecond Duration = 1000 * Microsecond
	Second      Duration = 1000 * Millisecond
	Minute      Duration = 60 * Second
	Hour        Duration = 60 * Minute
)

// Time is an instant in simulated time, expressed as nanoseconds elapsed
// since the simulation's zero instant. The zero value of Time is the start
// of the simulation.
type Time int64

// String renders t as elapsed simulated time since the zero instant.
func (t Time) String() string { return Duration(t).String() }

// IsZero reports whether t is the simulation's zero instant.
func (t Time) IsZero() bool { return t == 0 }

// Add returns t+d.
func (t Time) Add(d Duration) Time { return t + Time(d) }

// Sub returns the duration from t to u (u - t).
func (t Time) Sub(u Time) Duration { return Duration(u - t) }

// Before reports whether t is strictly earlier than u.
func (t Time) Before(u Time) bool { return t < u }

// After reports whether t is strictly later than u.
func (t Time) After(u Time) bool { return t > u }

// Seconds renders d as a float64 number of seconds, for human-facing
// output such as latency metrics.
func (d Duration) Seconds() float64 { return float64(d) / float64(Second) }

// Millis renders d as a float64 number of milliseconds.
func (d Duration) Millis() float64 { return float64(d) / float64(Millisecond) }

// String renders d in the most compact human-readable unit with up to
// three decimals and trailing zeros trimmed (e.g. "1.5ms", "200s", "42ns").
func (d Duration) String() string {
	sign := ""
	if d < 0 {
		sign = "-"
		d = -d
	}
	var unit string
	var v float64
	switch {
	case d >= Second:
		unit, v = "s", float64(d)/float64(Second)
	case d >= Millisecond:
		unit, v = "ms", float64(d)/float64(Millisecond)
	case d >= Microsecond:
		unit, v = "µs", float64(d)/float64(Microsecond)
	default:
		unit, v = "ns", float64(d)
	}
	return sign + trimFloat(v) + unit
}

// trimFloat formats with up to three decimals, trimming trailing zeros and
// a trailing dot (e.g. 1.500 -> "1.5", 200.000 -> "200").
func trimFloat(f float64) string {
	s := strconv.FormatFloat(f, 'f', 3, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	return s
}

// String renders the clock position, primarily for debugging output.
func (c *Clock) String() string { return fmt.Sprintf("@%s", c.now) }
