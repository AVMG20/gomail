// Package safe runs goroutines that recover (and log) panics instead of
// crashing the app.
package safe

import (
	"fmt"
	"log/slog"
	"runtime/debug"
)

// Recover logs a panic; use as `defer safe.Recover("where")`.
func Recover(where string) {
	if r := recover(); r != nil {
		slog.Error("recovered from panic", "where", where, "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
	}
}

// Go runs f in a goroutine with panic recovery.
func Go(where string, f func()) {
	go func() {
		defer Recover(where)
		f()
	}()
}

// Call runs f and converts a panic into an error.
func Call(where string, f func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("recovered from panic", "where", where, "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
			err = fmt.Errorf("internal error in %s: %v", where, r)
		}
	}()
	return f()
}
