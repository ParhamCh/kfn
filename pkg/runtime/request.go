package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// Request is the transport-agnostic view of an incoming invocation passed to a
// Handler. It is intentionally small so that non-HTTP triggers (queues, cron) can
// be added later without changing the Handler signature.
type Request struct {
	Method  string
	Path    string
	Headers http.Header
	Query   url.Values
	Body    []byte
}

// Response is what a Handler returns. A nil Response together with a nil error is
// treated as 204 No Content.
type Response struct {
	Status  int
	Headers http.Header
	Body    []byte
}

// Handler is the contract user functions implement. A returned error maps to a 500
// response (or to the status of a *HTTPError). The context carries the per-invocation
// deadline and is cancelled when the client disconnects.
type Handler func(ctx context.Context, req *Request) (*Response, error)

// Text builds a text/plain response.
func Text(status int, s string) *Response {
	return Bytes(status, "text/plain; charset=utf-8", []byte(s))
}

// JSON builds an application/json response by marshalling v. A marshalling failure is
// returned as an error so it can be propagated straight from a Handler:
//
//	return runtime.JSON(200, payload)
func JSON(status int, v any) (*Response, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("runtime: marshal json response: %w", err)
	}
	return Bytes(status, "application/json; charset=utf-8", b), nil
}

// Bytes builds a response with an explicit content type.
func Bytes(status int, contentType string, b []byte) *Response {
	h := http.Header{}
	h.Set("Content-Type", contentType)
	return &Response{Status: status, Headers: h, Body: b}
}

// HTTPError is an error that carries an explicit HTTP status. Handlers may return one
// to control the failure status code; any other error becomes a 500.
type HTTPError struct {
	Status  int
	Message string
}

func (e *HTTPError) Error() string { return e.Message }

// Errorf returns an *HTTPError with the given status and a formatted message.
func Errorf(status int, format string, args ...any) *HTTPError {
	return &HTTPError{Status: status, Message: fmt.Sprintf(format, args...)}
}
