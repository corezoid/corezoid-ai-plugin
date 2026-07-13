package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Retry tuning for the Corezoid API HTTP client. Only transient 429/503
// responses are retried — 4xx (other than 429) and 5xx (other than 503) are
// surfaced immediately because retrying them just wastes time.
//
// The "Var" suffix on the timing knobs is a tell that tests rewrite them; the
// values are package-level vars so test setup can dial them down to milliseconds.
const apiMaxAttempts = 3

var (
	apiRetryBaseDelayVar = 500 * time.Millisecond
	apiRetryMaxDelayVar  = 5 * time.Second
)

// req sends an authenticated JSON-RPC request to the Corezoid API.
func (v *Executor) req(method string, ops []map[string]any) (map[string]interface{}, error) {
	// Personal-workspace accounts have no companyID. The callers in this file
	// unconditionally inject `"company_id": v.WorkspaceID` into every op, so when
	// WorkspaceID is empty the payload carries `"company_id": ""` and Corezoid
	// rejects every request with `Value is not valid / company_id`. Drop the
	// empty placeholder (and its mirrors) so the request matches what the API
	// accepts for personal accounts. No-op for normal company workspaces.
	if strings.TrimSpace(v.WorkspaceID) == "" {
		for _, op := range ops {
			for _, k := range []string{"company_id", "from_company_id", "to_company_id"} {
				if s, ok := op[k].(string); ok && s == "" {
					delete(op, k)
				}
			}
		}
	}

	payload := map[string]any{"ops": ops}
	payloadJSON, _ := json.Marshal(payload)

	if v.Debug {
		var prettyJSON bytes.Buffer
		json.Indent(&prettyJSON, redactForLog(payloadJSON), "", "  ")
		logger.Debug("API Request, method=%s, payload=%s", method, prettyJSON.String())
	}

	path := "json"
	switch method {
	case "export_process":
		path = "download"
	case "compare", "merge":
		// The stage compare/merge (deploy) admin ops have their own endpoints —
		// /api/2/compare and /api/2/merge — not the shared /api/2/json.
		path = method
	}
	authURL := fmt.Sprintf("%s/api/2/%s", v.APIUrl, path)

	if v.Debug {
		logger.Debug("Request URL, url=%s", authURL)
	}

	client := newHTTPClient()
	resp, body, err := doWithRetry(v.Ctx, client, "POST", authURL, payloadJSON, v.Token, v.Debug)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if v.Debug {
		var prettyJSON bytes.Buffer
		if json.Indent(&prettyJSON, redactForLog(body), "", "  ") == nil {
			logger.Debug("API Response, method=%s, status=%s, body=%s", method, resp.Status, prettyJSON.String())
		} else {
			logger.Debug("API Response, method=%s, status=%s, body=(non-JSON, omitted)", method, resp.Status)
		}
	}

	var response map[string]interface{}
	err = json.Unmarshal(body, &response)
	if err != nil {
		logger.Error("Error parsing response: %v", err)
		return nil, err
	}
	err = v.checkError(response)
	if err != nil {
		return response, err
	}
	return response, nil
}

// newHTTPClient returns an http.Client. TLS certificate verification is
// disabled only when COREZOID_INSECURE_TLS=1 is set in the environment.
func newHTTPClient() *http.Client {
	if !insecureTLS {
		return &http.Client{}
	}
	logger.Warn("TLS certificate verification disabled (COREZOID_INSECURE_TLS=1) — do not use in production")
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
	}
}

// doWithRetry sends an authenticated request and retries transient 429/503
// responses with exponential backoff and jitter. Non-retriable statuses (and
// non-network errors) are returned to the caller after the first attempt;
// retrying e.g. a 4xx auth failure would just delay the user-visible error.
//
// The response body is rebuilt from `payloadJSON` on each attempt, so the body
// must be idempotent — which it is, since Corezoid's JSON-RPC ops are.
//
// Returns the final response and the already-read body. Caller owns resp.Body
// and must close it.
func doWithRetry(ctx context.Context, client *http.Client, method, url string, payloadJSON []byte, token string, debug bool) (*http.Response, []byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var lastErr error
	delay := apiRetryBaseDelayVar
	for attempt := 1; attempt <= apiMaxAttempts; attempt++ {
		// Re-check cancellation before each attempt so a cancelled context
		// fails fast even if we're mid-backoff.
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(payloadJSON))
		if err != nil {
			// NewRequest fails only on programmer error (bad method/URL),
			// not on transient I/O — no point retrying.
			return nil, nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", fmt.Sprintf("Simulator %s", token))
		if debug && attempt == 1 {
			safeHeaders := req.Header.Clone()
			safeHeaders.Set("Authorization", "Simulator ***")
			logger.Debug("Header: %v", safeHeaders)
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			logger.Error("Error making request (attempt %d/%d): %v", attempt, apiMaxAttempts, err)
			// Network errors aren't retried here — typed-error retry would
			// require teasing apart "temporary" vs "permanent" failures, and
			// in practice the Corezoid API surfaces overload as 429/503.
			return nil, nil, err
		}

		// 429 / 503 are the only statuses worth retrying — others (4xx, other
		// 5xx) won't get fixed by waiting.
		if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode != http.StatusServiceUnavailable {
			body, readErr := io.ReadAll(resp.Body)
			if readErr != nil {
				resp.Body.Close()
				logger.Error("Error reading response: %v", readErr)
				return nil, nil, readErr
			}
			return resp, body, nil
		}

		// Retriable. Drain and close the body so the connection can be reused.
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()

		if attempt == apiMaxAttempts {
			lastErr = fmt.Errorf("API returned %d after %d attempts", resp.StatusCode, attempt)
			break
		}

		wait := delay + jitter(delay)
		if retryAfter > 0 && retryAfter > wait {
			wait = retryAfter
		}
		logger.Warn("API status %d on attempt %d/%d — retrying in %s", resp.StatusCode, attempt, apiMaxAttempts, wait)
		// Sleep but surface cancellation immediately. A bare time.Sleep would
		// keep the goroutine alive long past a /cancel notification.
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, nil, ctx.Err()
		case <-timer.C:
		}

		delay *= 2
		if delay > apiRetryMaxDelayVar {
			delay = apiRetryMaxDelayVar
		}
	}
	return nil, nil, lastErr
}

// parseRetryAfter returns the duration encoded in a Retry-After header. Only
// the integer-seconds form is supported (RFC 7231 also allows HTTP-date, but
// Corezoid only sends seconds). Returns 0 on parse failure.
func parseRetryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	secs, err := strconv.Atoi(strings.TrimSpace(h))
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

// jitter returns a small random offset (up to 25% of d) to spread out
// retry storms when many clients hit a 429 at the same time. Returns 0 for
// non-positive d — guards against rand.Int63n's panic on n <= 0 if tests
// dial the delay down to a sub-nanosecond value.
func jitter(d time.Duration) time.Duration {
	max := int64(d) / 4
	if max <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(max)) //nolint:gosec
}

// checkError inspects a Corezoid JSON-RPC response envelope and converts any
// failure status into a Go error. The prefixes are intentionally neutral
// ("API error" / "API op error") because this function runs for every API
// call — push, delete, list, deploy, commit, etc. — and the previous
// "failed to compile API code" label misled callers who saw it for unrelated
// operations like a failed delete or a bad list query.
func (v *Executor) checkError(rsp map[string]interface{}) error {
	if rsp == nil {
		return fmt.Errorf("API error: no response from server")
	}

	if requestProc, ok := rsp["request_proc"].(string); !ok || requestProc != "ok" {
		return fmt.Errorf("API error: request_proc not ok")
	}

	if opsArray, ok := rsp["ops"].([]interface{}); ok {
		for _, op := range opsArray {
			if opMap, ok := op.(map[string]interface{}); ok {
				if proc, ok := opMap["proc"].(string); !ok || proc != "ok" {
					if errors, ok := opMap["errors"].(map[string]interface{}); ok {
						var errorMsgs []string
						for objID, errMsgs := range errors {
							nodeID := objID
							if errArray, ok := errMsgs.([]interface{}); ok {
								for _, errMsg := range errArray {
									if msg, ok := errMsg.(string); ok {
										errorMsgs = append(errorMsgs, fmt.Sprintf("Node %s: %s", nodeID, msg))
									}
								}
							}
						}
						if len(errorMsgs) > 0 {
							return fmt.Errorf("API op error:\n%s", strings.Join(errorMsgs, "\n"))
						}
					}

					errorMsg := "unknown error"
					if msg, ok := opMap["description"].(string); ok {
						errorMsg = msg
					}
					return fmt.Errorf("API op error: %s", errorMsg)
				}
			}
		}
	}
	return nil
}
