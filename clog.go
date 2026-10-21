package clog

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Middleware is a canonical logging middleware.
//
// It adds a line with the method, path, duration, and remote address.
//
//	LogCookieNames()
//
// Adds the names of cookies received or set by the server to the log line.
//
//	WithLogger(*slog.Logger)
//
// Allows you to pass in a slog instance. Without this the middleware uses slog.Default.
//
// Panics are recovered, logged, and then re-paniced. You should recover in an upstream middleware.
func Middleware(optFns ...Option) func(http.Handler) http.Handler {
	opts := &options{
		logger: slog.Default(),
	}

	for _, fn := range optFns {
		fn(opts)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			defer func() {
				panicErr := recover()
				if panicErr != nil {
					opts.logger.ErrorContext(r.Context(), "http request recovered from panic",
						"method", r.Method,
						"path", r.URL.Path,
						"remote_addr", r.RemoteAddr,
						"duration", time.Since(start),
						"err", panicErr,
					)

					panic(panicErr) // re-panic. We only want to log the error
				}
			}()

			wCapture := &captureResponseWriter{ResponseWriter: w}
			ctxCapture := &contextValue{}
			r = r.WithContext(context.WithValue(r.Context(), contextKey{}, ctxCapture))

			next.ServeHTTP(wCapture, r)

			status := 200
			if wCapture.status != nil {
				status = *wCapture.status
			}

			setCookies := wCapture.Header().Values("set-cookie")
			for idx, cookie := range setCookies {
				n, _, _ := strings.Cut(cookie, "=")
				setCookies[idx] = n
			}

			cookies := r.Cookies()
			reqCookies := make([]string, len(cookies))
			for i, cookie := range cookies {
				reqCookies[i] = cookie.Name
			}

			args := []slog.Attr{
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.String("remote_addr", r.RemoteAddr),
				slog.Int("status", status),
			}

			if opts.logCookieNames {
				if len(reqCookies) > 0 {
					args = append(args, slog.String("cookies", strings.Join(reqCookies, ",")))
				}
				if len(setCookies) > 0 {
					args = append(args, slog.String("set_cookies", strings.Join(setCookies, ",")))
				}
			}

			// Add the attrs from the downstream handlers
			ctxCapture.mx.Lock()
			defer ctxCapture.mx.Unlock()
			for _, a := range ctxCapture.attrs {
				args = append(args, a)
			}

			// Add the duration last
			args = append(args, slog.Duration("duration", time.Since(start)))

			if status >= 400 {
				opts.logger.LogAttrs(r.Context(), slog.LevelError, "http request error", args...)
				return
			}

			opts.logger.LogAttrs(r.Context(), slog.LevelInfo, "http request processed", args...)
		})
	}
}

// Add adds slog Attrs to the canonical log line.
//
// The context passed must be downstream of clog.Middleware(). If not, it will noop.
func Add(ctx context.Context, args ...any) {
	v, ok := ctx.Value(contextKey{}).(*contextValue)
	if !ok {
		// Silently bypass if value is not in context
		return
	}

	v.add(args...)
}

type (
	contextKey   struct{}
	contextValue struct {
		mx    sync.Mutex
		attrs []slog.Attr
	}
)

func (v *contextValue) add(args ...any) {
	v.mx.Lock()
	defer v.mx.Unlock()

	var a slog.Attr
	for len(args) > 0 {
		a, args = argsToAttr(args)
		v.attrs = append(v.attrs, a)
	}
}

type captureResponseWriter struct {
	http.ResponseWriter

	status *int
}

func (w *captureResponseWriter) WriteHeader(status int) {
	w.status = &status
	w.ResponseWriter.WriteHeader(status)
}
