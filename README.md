# Clog is a Canonical Log

## Middleware
 
The middleware captures the request metadata (path, method, and remote address), and response metadata (status code, duration) and logs a single line.
It wraps the next handler with a `recover()` to log a line even when the downstream handlers panic.
