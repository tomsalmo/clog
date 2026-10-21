package clog_test

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"testing"
)

// captureHandler is a log/slog handler that just captures all the log records it receives.
//
// It has a mutex on writing records for concurrency safety.
type captureHandler struct {
	records []slog.Record
	mx      sync.Mutex
}

// Basic noop
func (*captureHandler) Enabled(context.Context, slog.Level) bool { return true }

// Capture the records
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mx.Lock()
	defer h.mx.Unlock()

	h.records = append(h.records, r)
	return nil
}

// Basic nnop
func (h *captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler { return h }

// Basic noop
func (h *captureHandler) WithGroup(name string) slog.Handler { return h }

func recoverer(t *testing.T, wantPanic bool) func(next http.Handler) http.Handler {
	t.Helper()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				err := recover()
				if wantPanic && err == nil {
					t.Errorf("handler didn't panic, want panic")
				}
				if !wantPanic && err == nil {
					t.Errorf("handler panic = %q, want no panic", err)
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}
