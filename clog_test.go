package clog_test

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/tomsalmo/clog"
)

func TestPanic(t *testing.T) {
	var handlerErr = fmt.Sprintf("panic handler %d", rand.Int64())

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
	var gotRemoteAddr, gotDuration string
	got.Attrs(func(a slog.Attr) bool {
		if a.Key == "remote_addr" {
			gotRemoteAddr = a.Value.String()
			return true
		}
		if a.Key == "duration" {
			gotDuration = a.Value.String()
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
	if gotRemoteAddr == "" {
		t.Errorf("%q = %q, want value", "remote_addr", gotRemoteAddr)
	}
	if gotDuration == "" {
		t.Errorf("%q = %q, want value", "duration", gotDuration)
	}
}

func TestMiddlewareOK(t *testing.T) {
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	cap := &captureHandler{}
	l := slog.New(cap)

	h := clog.Middleware(clog.WithLogger(l))(okHandler)

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
		t.Fatalf("captured log records len = %d, want 1", len(cap.records))
	}

	got := cap.records[0]
	if got.Level != slog.LevelInfo {
		t.Errorf("level = %v, want %v", got.Level, slog.LevelInfo)
	}
	if got.Message != "http request processed" {
		t.Errorf("message = %q, want %q", got.Message, "http request processed")
	}

	gotAttrs := recordAttrs(got)
	wantKeys := []string{"method", "path", "remote_addr", "status", "duration"}
	if diff := cmp.Diff(wantKeys, attrKeys(gotAttrs)); diff != "" {
		t.Errorf("attr keys does not match expectation (-want,+got)\n%s", diff)
	}

	gotVals := attrVals(gotAttrs)
	if gotVals["remote_addr"] == "" {
		t.Errorf("%q = %q, want value", "remote_addr", gotVals["remote_addr"])
	}
	delete(gotVals, "remote_addr")
	delete(gotVals, "duration")

	wantVals := map[string]any{
		"method": "GET",
		"path":   "/",
		"status": int64(http.StatusOK),
	}
	if diff := cmp.Diff(wantVals, gotVals); diff != "" {
		t.Errorf("log attrs does not match expectation (-want,+got)\n%s", diff)
	}

	var lastAttr slog.Attr
	got.Attrs(func(a slog.Attr) bool {
		lastAttr = a
		return true
	})
	if lastAttr.Key != "duration" {
		t.Errorf("last attr key = %q, want %q", lastAttr.Key, "duration")
	}
	if lastAttr.Value.Kind() != slog.KindDuration {
		t.Errorf("duration value kind = %v, want %v", lastAttr.Value.Kind(), slog.KindDuration)
	}
}

func TestMiddlewareErrorStatus(t *testing.T) {
	errHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clog.Add(r.Context(), "err_code", "ENOENT")
		http.Error(w, "not found", http.StatusNotFound)
	})

	cap := &captureHandler{}
	l := slog.New(cap)

	h := clog.Middleware(clog.WithLogger(l))(errHandler)

	s := httptest.NewTestServer(t, h)
	s.Start()
	defer s.Close()

	resp, err := s.Client().Get(s.URL)
	if err != nil {
		t.Fatalf("error performing GET / against httptest server: %v", err)
	}
	resp.Body.Close()

	cap.mx.Lock()
	defer cap.mx.Unlock()

	if len(cap.records) != 1 {
		t.Fatalf("captured log records len = %d, want 1", len(cap.records))
	}

	got := cap.records[0]
	if got.Level != slog.LevelError {
		t.Errorf("level = %v, want %v", got.Level, slog.LevelError)
	}
	if got.Message != "http request error" {
		t.Errorf("message = %q, want %q", got.Message, "http request error")
	}

	gotAttrs := recordAttrs(got)
	wantKeys := []string{"method", "path", "remote_addr", "status", "err_code", "duration"}
	if diff := cmp.Diff(wantKeys, attrKeys(gotAttrs)); diff != "" {
		t.Errorf("attr keys does not match expectation (-want,+got)\n%s", diff)
	}

	gotVals := attrVals(gotAttrs)
	if gotVals["remote_addr"] == "" {
		t.Errorf("%q = %q, want value", "remote_addr", gotVals["remote_addr"])
	}
	delete(gotVals, "remote_addr")
	delete(gotVals, "duration")

	wantVals := map[string]any{
		"method":   "GET",
		"path":     "/",
		"status":   int64(http.StatusNotFound),
		"err_code": "ENOENT",
	}
	if diff := cmp.Diff(wantVals, gotVals); diff != "" {
		t.Errorf("log attrs does not match expectation (-want,+got)\n%s", diff)
	}
}

type attrKV struct {
	Key string
	Val any
}

func recordAttrs(r slog.Record) []attrKV {
	out := make([]attrKV, 0, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		out = append(out, attrKV{Key: a.Key, Val: a.Value.Any()})
		return true
	})
	return out
}

func attrKeys(as []attrKV) []string {
	keys := make([]string, len(as))
	for i, a := range as {
		keys[i] = a.Key
	}
	return keys
}

func attrVals(as []attrKV) map[string]any {
	vals := make(map[string]any, len(as))
	for _, a := range as {
		vals[a.Key] = a.Val
	}
	return vals
}

func TestAdd(t *testing.T) {
	tests := []struct {
		name string
		args []any
		want []attrKV
	}{
		{
			name: "key value pair",
			args: []any{"user_id", 42},
			want: []attrKV{{Key: "user_id", Val: int64(42)}},
		},
		{
			name: "slog attr",
			args: []any{slog.String("tenant", "acme")},
			want: []attrKV{{Key: "tenant", Val: "acme"}},
		},
		{
			name: "mixed key value pairs and slog attrs",
			args: []any{"user_id", 42, slog.String("tenant", "acme")},
			want: []attrKV{
				{Key: "user_id", Val: int64(42)},
				{Key: "tenant", Val: "acme"},
			},
		},
		{
			name: "value with missing key",
			args: []any{42},
			want: []attrKV{{Key: "!BADKEY", Val: int64(42)}},
		},
		{
			name: "key with missing value",
			args: []any{"orphan"},
			want: []attrKV{{Key: "!BADKEY", Val: "orphan"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			addHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				clog.Add(r.Context(), tc.args...)
				w.WriteHeader(http.StatusOK)
			})

			cap := &captureHandler{}
			l := slog.New(cap)

			h := clog.Middleware(clog.WithLogger(l))(addHandler)

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
				t.Fatalf("captured log records len = %d, want 1", len(cap.records))
			}

			gotAttrs := recordAttrs(cap.records[0])

			wantKeys := append([]string{"method", "path", "remote_addr", "status"}, attrKeys(tc.want)...)
			wantKeys = append(wantKeys, "duration")
			if diff := cmp.Diff(wantKeys, attrKeys(gotAttrs)); diff != "" {
				t.Errorf("attr keys does not match expectation (-want,+got)\n%s", diff)
			}

			wantVals := attrVals(tc.want)
			wantVals["method"] = "GET"
			wantVals["path"] = "/"
			wantVals["status"] = int64(http.StatusOK)

			gotVals := attrVals(gotAttrs)
			if gotVals["remote_addr"] == "" {
				t.Errorf("%q = %q, want value", "remote_addr", gotVals["remote_addr"])
			}
			if gotVals["duration"] == nil {
				t.Errorf("%q = %v, want value", "duration", gotVals["duration"])
			}
			delete(gotVals, "remote_addr")
			delete(gotVals, "duration")

			if diff := cmp.Diff(wantVals, gotVals); diff != "" {
				t.Errorf("log attrs does not match expectation (-want,+got)\n%s", diff)
			}
		})
	}
}

func TestAddNoop(t *testing.T) {
	noopHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clog.Add(context.Background(), "user_id", 42)
		w.WriteHeader(http.StatusOK)
	})

	cap := &captureHandler{}
	l := slog.New(cap)

	h := clog.Middleware(clog.WithLogger(l))(noopHandler)

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
		t.Fatalf("captured log records len = %d, want 1", len(cap.records))
	}

	gotAttrs := recordAttrs(cap.records[0])
	wantKeys := []string{"method", "path", "remote_addr", "status", "duration"}
	if diff := cmp.Diff(wantKeys, attrKeys(gotAttrs)); diff != "" {
		t.Errorf("attr keys does not match expectation (-want,+got)\n%s", diff)
	}
}

func TestAddConcurrent(t *testing.T) {
	const workers = 10

	concurrentHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var wg sync.WaitGroup
		for i := range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				clog.Add(r.Context(), "worker", i)
			}()
		}
		wg.Wait()

		w.WriteHeader(http.StatusOK)
	})

	cap := &captureHandler{}
	l := slog.New(cap)

	h := clog.Middleware(clog.WithLogger(l))(concurrentHandler)

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
		t.Fatalf("captured log records len = %d, want 1", len(cap.records))
	}

	gotAttrs := recordAttrs(cap.records[0])

	if len(gotAttrs) != 4+workers+1 {
		t.Errorf("captured attrs len = %d, want %d", len(gotAttrs), 4+workers+1)
	}

	seen := make(map[int64]bool, workers)
	for _, a := range gotAttrs {
		if a.Key == "worker" {
			seen[a.Val.(int64)] = true
		}
	}
	if len(seen) != workers {
		t.Errorf("distinct worker attrs = %d, want %d", len(seen), workers)
	}
	for i := range workers {
		if !seen[int64(i)] {
			t.Errorf("missing worker attr for worker %d", i)
		}
	}
}
