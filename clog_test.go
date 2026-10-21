package clog_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/tomsalmo/clog"
)

func TestPanic(t *testing.T) {
	const handlerErr = "panic handler"

	panicHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(handlerErr)
	})

	cap := &captureHandler{}
	l := slog.New(cap)

	// Wrap the log mw in a panic helper to catch repanic
	h := recoverer(t, true)(
		clog.Middleware(clog.WithLogger(l))(
			panicHandler,
		),
	)

	// Do a test request
	s := httptest.NewTestServer(t, h)
	s.Start()
	defer s.Close()
	_, err := s.Client().Get(s.URL)
	if err != nil {
		t.Fatalf("error performing GET / against httptest server: %v", err)
	}

	cap.mx.Lock()
	defer cap.mx.Unlock()

	if len(cap.records) != 1 {
		t.Errorf("captured log records len = %d, want 1", len(cap.records))
	}

	// Test the record properties
	got := cap.records[0]
	want := slog.Record{
		Level:   slog.LevelError,
		Message: "http request recovered from panic",
	}
	if diff := cmp.Diff(
		want, got,
		cmpopts.IgnoreUnexported(slog.Record{}),
		cmpopts.IgnoreFields(slog.Record{}, "PC", "Time"),
	); diff != "" {
		t.Errorf("log line does not match expectation (-want,+got)\n%s", diff)
	}

	// Test the expected attrs
	gotAttrs := make(map[string]any, got.NumAttrs())
	got.Attrs(func(a slog.Attr) bool {
		// Exclude remote_addr, I don't know how to test it
		if a.Key == "remote_addr" {
			return true
		}

		gotAttrs[a.Key] = a.Value.Any()
		return true
	})
	wantAttrs := map[string]any{
		"method": "GET",
		"path":   "/",
		"err":    handlerErr,
	}
	if diff := cmp.Diff(wantAttrs, gotAttrs); diff != "" {
		t.Errorf("log attrs does not match expectation (-want,+got)\n%s", diff)
	}
}
