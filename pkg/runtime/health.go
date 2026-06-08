package runtime

import (
	"net/http"
	"sync/atomic"
)

// health tracks liveness and readiness state for the probe endpoints.
//
// Liveness answers "is the process alive" and stays true for the life of the
// process. Readiness answers "should traffic be routed here"; M2 flips it to false
// on SIGTERM before draining so Kubernetes stops sending new requests while in-flight
// ones finish. For M1 the process is ready as soon as the server is listening.
type health struct {
	ready atomic.Bool
}

func (h *health) setReady(ready bool) { h.ready.Store(ready) }

// liveness reports OK as long as the process is serving.
func (h *health) liveness(w http.ResponseWriter, _ *http.Request) {
	writePlain(w, http.StatusOK, "ok")
}

// readiness reports OK only while the instance should receive traffic.
func (h *health) readiness(w http.ResponseWriter, _ *http.Request) {
	if h.ready.Load() {
		writePlain(w, http.StatusOK, "ready")
		return
	}
	writePlain(w, http.StatusServiceUnavailable, "not ready")
}

func writePlain(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}
