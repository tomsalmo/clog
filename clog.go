package clog

import (
	"log/slog"
	"net/http"
	"strings"
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
			defer func() {
				panicErr := recover()
				if panicErr != nil {
					opts.logger.ErrorContext(r.Context(), "http request recovered from panic",
						"method", r.Method,
						"path", r.URL.Path,
						"remote_addr", r.RemoteAddr,
						"err", panicErr,
					)
				}
				panic(panicErr) // re-panic. We only want to log the error
			}()

			start := time.Now()
			wCapture := &captureResponseWriter{ResponseWriter: w}

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

			args := []any{
				"method", r.Method,
				"path", r.URL.Path,
				"remote_addr", r.RemoteAddr,
				"status", status,
				"duration", time.Since(start),
			}

			if opts.logCookieNames {
				if len(reqCookies) > 0 {
					args = append(args, "cookies", strings.Join(reqCookies, ","))
				}
				if len(setCookies) > 0 {
					args = append(args, "set_cookies", strings.Join(setCookies, ","))
				}
			}

			if status >= 400 {
				opts.logger.ErrorContext(r.Context(), "http request error", args...)
				return
			}

			opts.logger.InfoContext(r.Context(), "http request processed", args...)
		})
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
