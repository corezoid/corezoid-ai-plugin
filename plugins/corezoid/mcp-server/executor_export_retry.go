package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// exportRetryDelaysVar is the wait before each repeat of a scheme export that
// failed transiently; its length is the number of repeats. A cold export of a
// large stage often overruns the platform's own export timeout while a warm
// repeat a few seconds later succeeds, so the first repeat comes quickly and
// the later ones give the platform more room. Tests dial these down.
var exportRetryDelaysVar = []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second}

// errExportStillTransient is wrapped into an export error that is still
// transient after every repeat. Its text is the hint that makes the model
// retry the tool instead of giving up on it; callers test for it with
// errors.Is.
var errExportStillTransient = errors.New("\nHint: the Corezoid platform timed out or failed transiently while exporting this object. " +
	"This is common on the first (cold) export of a large stage or folder; calling the same tool again usually succeeds.")

// transientExportMarkers are lower-cased fragments of the errors the platform
// returns when an export did not fit into its time budget or hit a passing
// internal failure. Anything else (not found, access denied, wrong obj_type)
// fails the same way on every attempt and is returned at once.
var transientExportMarkers = []string{
	"timeout",
	"timed out",
	"time out",
	"internal server error",
	"internal error",
	"bad gateway",
	"gateway time",
	"temporarily unavailable",
	"service unavailable",
	"connection reset",
	"unexpected eof",
	"api returned 429",
	"api returned 503",
	"http 500",
	"http 502",
	"http 503",
	"http 504",
}

// isTransientExportError reports whether a failed scheme export is worth
// repeating. A non-JSON body counts: a gateway that gives up on a slow export
// answers with an HTML error page.
func isTransientExportError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, m := range transientExportMarkers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}

// withExportRetry runs export, repeating it with backoff while it fails
// transiently. Exports are read-only, so a repeat cannot create anything. The
// error that survives every repeat wraps errExportStillTransient.
func withExportRetry[T any](v *Executor, what string, export func() (T, error)) (T, error) {
	var zero T
	for attempt := 0; ; attempt++ {
		res, err := export()
		if err == nil {
			return res, nil
		}
		if !isTransientExportError(err) {
			return zero, err
		}
		if attempt >= len(exportRetryDelaysVar) {
			return zero, fmt.Errorf("%w (after %d attempts)%w", err, attempt+1, errExportStillTransient)
		}
		if cerr := v.checkCancel(); cerr != nil {
			return zero, cerr
		}
		wait := exportRetryDelaysVar[attempt]
		logger.Warn("export of %s failed transiently (attempt %d/%d): %v — retrying in %s", what, attempt+1, len(exportRetryDelaysVar)+1, err, wait)
		ctx := v.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return zero, ctx.Err()
		case <-timer.C:
		}
	}
}
