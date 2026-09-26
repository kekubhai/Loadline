// Package engine implements LOADLINE's discrete-event simulation core.
//
// The engine is fully self-contained: it depends only on the Go standard
// library. No HTTP, database, WebSocket, UI, or cloud-provider code may be
// imported here. Simulation time is integer nanoseconds and advances only
// when events are processed — never by wall-clock sleeps.
package engine

// Duration is a span of simulated time in nanoseconds.
//
// Using a plain int64 (instead of time.Duration) makes the engine's clock
// independent of the wall clock: there is no way to accidentally mix
// simulated time with real time.
type Duration int64

// Common simulated-time durations. Simulation logic should compose these
// rather than inventing wall-clock time.
const (
	Nanosecond  Duration = 1
	Microsecond Duration = 1000 * Nanosecond
	Millisecond Duration = 1000 * Microsecond
	Second      Duration = 1000 * Millisecond
	Minute      Duration = 60 * Second
	Hour        Duration = 60 * Minute
)
type Time int64
func (t Time) IsZero() bool { return t == 0 }

func (t Time) Add(d Duration) Time { return t + Time(d) }

func (t Time) Sub(u Time) Duration { return Duration(u - t) }

func (t Time) Before(u Time) bool { return t < u }

// After reports whether t is strictly later than u.
func (t Time) After(u Time) bool { return t > u }

// Seconds renders d as a float64 number of seconds, for human-facing
// output such as latency metrics.
func (d Duration) Seconds() float64 { return float64(d) / float64(Second) }

// Millis renders d as a float64 number of milliseconds.
func (d Duration) Millis() float64 { return float64(d) / float64(Millisecond) }

// String renders d in the most compact human-readable unit.
func (d Duration) String() string {
	switch {
	case d == 0:
		return "0s"
	case d >= Second:
		return formatFloat(float64(d) / float64(Second)) + "s"
	case d >= Millisecond:
		return formatFloat(float64(d) / float64(Millisecond)) + "ms"
	case d >= Microsecond:
		return formatFloat(float64(d) / float64(Microsecond)) + "µs"
	default:
		return formatFloat(float64(d)) + "ns"
	}
}

func formatFloat(f float64) string {
	// Keep output short and stable: three decimals, trailing zeros trimmed.
	s := strconvFormatFloat(f)
	return s
}

func strconvFormatFloat(f float64) string {
	// Small helper to avoid pulling fmt into the hot path; fmt is fine, but
	// this keeps time.go free of heavier imports.
	// Formatting: round to 3 decimals, trim trailing zeros.
	rounded := float64(int64(f*1000+0.5)) / 1000
	if rounded == float64(int64(rounded)) {
		return itoa(int64(rounded))
	}
	// Non-integer: emit up to three decimals manually.
	digits := int64((rounded - float64(int64(rounded))) * 1000)
	whole := int64(rounded)
	return itoa(whole) + "." + trimZeros(pad3(digits))
}

func pad3(v int64) string {
	s := itoa(v)
	for len(s) < 3 {
		s = "0" + s
	}
	return s
}

func trimZeros(s string) string {
	for len(s) > 1 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	return s
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
