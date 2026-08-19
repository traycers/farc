// Package levellog adds a uniform level=INFO/WARN/ERROR prefix to the
// func(format string, args ...any) callback every long-lived farc component
// already logs through (SetLogger's convention, internal/farcd.Farcd,
// internal/hlsd.Hlsd, ...) -- a cross-binary concern, same as
// internal/tracing, so plugging this in needs no change to that signature
// anywhere else in the codebase (.scratch/observability/spec.md).
package levellog

// Logger wraps an existing logf callback, prepending a level token to every
// line it emits.
type Logger struct {
	logf func(format string, args ...any)
}

// New wraps logf. A nil logf is replaced with a no-op, matching every
// SetLogger's own default-no-op convention.
func New(logf func(format string, args ...any)) Logger {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return Logger{logf: logf}
}

// Info logs a routine lifecycle line (start/stop, access log).
func (l Logger) Info(format string, args ...any) { l.logf("level=INFO "+format, args...) }

// Warn logs a recoverable anomaly (reconnect, dropped frames, a timeout
// that isn't fatal).
func (l Logger) Warn(format string, args ...any) { l.logf("level=WARN "+format, args...) }

// Error logs an operation that actually failed (write verify failure, a
// fatal error reported just before the process exits).
func (l Logger) Error(format string, args ...any) { l.logf("level=ERROR "+format, args...) }
