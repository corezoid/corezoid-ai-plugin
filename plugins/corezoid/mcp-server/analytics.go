package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

const analyticsEndpoint = "https://www.corezoid.com/api/2/json/public/1852976/5b76d006818d63730bc18a5b0e7d8d091e82d2a2"
const analyticsConvID = 1852976
const analyticsBatchSize = 20
const analyticsFlushInterval = 5 * time.Second

var analyticsEnabled atomic.Bool
var analyticsTransport string
var installationID string
var analyticsCh chan AnalyticsEvent

// AnalyticsEvent holds telemetry data for a single tool call.
type AnalyticsEvent struct {
	Ts             string `json:"ts"`
	Tool           string `json:"tool"`
	DurationMs     int64  `json:"duration_ms"`
	IsError        bool   `json:"is_error"`
	ErrorType      string `json:"error_type,omitempty"`
	APIURL         string `json:"api_url,omitempty"`
	Transport      string `json:"transport"`
	ServerVersion  string `json:"server_version"`
	InstallationID string `json:"installation_id"`
}

// classifyError maps an error result string to one of the fixed error_type enum values.
// It never returns free-form text — only the predefined enum values.
func classifyError(result string) string {
	lower := strings.ToLower(result)
	switch {
	case strings.Contains(lower, "auth") || strings.Contains(lower, "token") ||
		strings.Contains(lower, "unauthorized") || strings.Contains(lower, "forbidden"):
		return "auth_error"
	case strings.Contains(lower, "validation") || strings.Contains(lower, "invalid") ||
		strings.Contains(lower, "lint"):
		return "validation_error"
	case strings.Contains(lower, "not found") || strings.Contains(lower, "404"):
		return "not_found"
	case strings.Contains(lower, "api") || strings.Contains(lower, "http") ||
		strings.Contains(lower, "request") || strings.Contains(lower, "fetch"):
		return "api_error"
	default:
		return "unknown"
	}
}

// hostnameOnly strips scheme, path, and query from a URL, returning just the host.
func hostnameOnly(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	if !strings.Contains(rawURL, "://") {
		rawURL = "https://" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// loadOrCreateInstallationID reads ~/.corezoid/installation_id or generates and
// persists a fresh UUID v4. Falls back to an in-memory UUID if the file cannot be written.
func loadOrCreateInstallationID() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return generateUUIDv4()
	}
	dir := filepath.Join(home, ".corezoid")
	path := filepath.Join(dir, "installation_id")

	if data, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(data)); len(id) == 36 {
			return id
		}
	}

	id := generateUUIDv4()
	if err := os.MkdirAll(dir, 0700); err == nil {
		_ = os.WriteFile(path, []byte(id+"\n"), 0600)
	}
	return id
}

// generateUUIDv4 produces a random UUID v4 using crypto/rand.
func generateUUIDv4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	)
}

// initAnalytics loads the installation ID and starts the background sender if
// COREZOID_ANALYTICS=true. Must be called after loadConfig() and before the
// MCP server starts accepting requests.
func initAnalytics() {
	if os.Getenv("COREZOID_ANALYTICS_DISABLED") != "" {
		return
	}
	installationID = loadOrCreateInstallationID()
	analyticsCh = make(chan AnalyticsEvent, 100)
	analyticsEnabled.Store(true)
	go runAnalyticsSender()
	logger.Debug("analytics: enabled, installation_id=%s transport=%s", installationID, analyticsTransport)
}

// emitAnalyticsEvent enqueues an event for async delivery.
// Non-blocking: events are dropped silently when the channel is full.
func emitAnalyticsEvent(e AnalyticsEvent) {
	if !analyticsEnabled.Load() {
		return
	}
	select {
	case analyticsCh <- e:
	default:
	}
}

// runAnalyticsSender drains analyticsCh and flushes batches every 5 s or 20 events.
func runAnalyticsSender() {
	ticker := time.NewTicker(analyticsFlushInterval)
	defer ticker.Stop()

	batch := make([]AnalyticsEvent, 0, analyticsBatchSize)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		sendBatch(batch)
		batch = batch[:0]
	}

	for {
		select {
		case e := <-analyticsCh:
			batch = append(batch, e)
			if len(batch) >= analyticsBatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

type analyticsOp struct {
	Type   string         `json:"type"`
	Obj    string         `json:"obj"`
	ConvID int            `json:"conv_id"`
	Ref    string         `json:"ref"`
	Data   AnalyticsEvent `json:"data"`
}

// sendBatch POSTs events to the analytics endpoint using the Corezoid ops API.
// Failures are logged at DEBUG level only — never surfaced to the user.
func sendBatch(events []AnalyticsEvent) {
	ops := make([]analyticsOp, len(events))
	for i, e := range events {
		ops[i] = analyticsOp{
			Type:   "create",
			Obj:    "task",
			ConvID: analyticsConvID,
			Ref:    fmt.Sprintf("%s-%s-%d", e.InstallationID, e.Ts, i),
			Data:   e,
		}
	}
	payload := map[string]any{"ops": ops}
	data, err := json.Marshal(payload)
	if err != nil {
		logger.Debug("analytics: marshal error: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, analyticsEndpoint, bytes.NewReader(data))
	if err != nil {
		logger.Debug("analytics: build request error: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		logger.Debug("analytics: send error: %v", err)
		return
	}
	defer resp.Body.Close()
	logger.Debug("analytics: sent %d events, status=%d", len(events), resp.StatusCode)
}
