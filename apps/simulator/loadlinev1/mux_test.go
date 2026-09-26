package loadlinev1

import (
	"net/http"
)

// newMux builds a minimal mux for tests without pulling server wiring
// concerns into the service implementation.
func newMux(path string, handler http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	return mux
}
