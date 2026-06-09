package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

// requestIDHeader is the canonical header used to carry a request id in and out.
const requestIDHeader = "X-Request-Id"

// ctxKey is an unexported context key type so callers cannot collide with it.
type ctxKey int

const requestIDKey ctxKey = iota

// RequestID returns the request id for the current invocation, or "" if none is set.
// Handlers can use it to correlate their own logs with the runtime's access log.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// withRequestID ensures every request has an id: it honors an inbound X-Request-Id when
// present, otherwise generates one. The id is placed on the request context (for the
// handler and the access log) and echoed on the response header.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set(requestIDHeader, id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// newRequestID returns a random 128-bit hex id.
func newRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never returns an error on supported platforms
	return hex.EncodeToString(b[:])
}
