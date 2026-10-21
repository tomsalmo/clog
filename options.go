package clog

import "log/slog"

type (
	options struct {
		logCookieNames bool
		logger         *slog.Logger
	}

	Option func(*options)
)

// LogCookieNames configures clog to log the names of all cookies sent on the request, or set by the response.
func LogCookieNames(o *options) {
	o.logCookieNames = true
}

// WithLogger uses the supplied *slog.Logger, instead of the default log/slog logger
func WithLogger(l *slog.Logger) func(*options) {
	return func(o *options) {
		o.logger = l
	}
}
