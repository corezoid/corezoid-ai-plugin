package main

import (
	"context"
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

// exportRetryBudgetVar caps how long after the first attempt a further repeat
// may still start. A platform timeout can itself take minutes, so counting
// attempts alone lets one pull run for many minutes, long after the client has
// given up on the tool call and the model has called it again. The first
// repeat is always made, since it is the one that usually succeeds.
var exportRetryBudgetVar = 2 * time.Minute

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
// repeating. A non-JSON body counts only with a 5xx status: a gateway that
// gives up on a slow export answers with an HTML error page, but so do a WAF
// block or a wrong API URL (4xx), and repeating those only delays the real
// error behind a "retry the tool" hint.
func isTransientExportError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var nonJSON *nonJSONResponseError
	if errors.As(err, &nonJSON) {
		return nonJSON.Status >= 500
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
// transiently, until the repeats or exportRetryBudgetVar run out. Exports are
// read-only, so a repeat cannot create anything. The error that survives every
// repeat wraps errExportStillTransient.
func withExportRetry[T any](v *Executor, what string, export func() (T, error)) (T, error) {
	var zero T
	start := time.Now()
	for attempt := 0; ; attempt++ {
		res, err := export()
		if err == nil {
			return res, nil
		}
		if !isTransientExportError(err) {
			return zero, err
		}
		if attempt >= len(exportRetryDelaysVar) ||
			(attempt > 0 && time.Since(start)+exportRetryDelaysVar[attempt] > exportRetryBudgetVar) {
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
