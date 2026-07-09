package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"time"
)

// FeedbackEvent is the payload sent to the feedback Corezoid process.
type FeedbackEvent struct {
	Ts                string `json:"ts"`
	Problem           string `json:"problem"`
	Expected          string `json:"expected,omitempty"`
	ProposedSolution  string `json:"proposed_solution,omitempty"`
	Tool              string `json:"tool,omitempty"`
	TranscriptExcerpt string `json:"transcript_excerpt,omitempty"`
	Contact           string `json:"contact,omitempty"`
	Transport         string `json:"transport"`
	ServerVersion     string `json:"server_version"`
	InstallationID    string `json:"installation_id"`
}

// Compiled redaction patterns — evaluated once at startup.
var (
	reBearerToken = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9+/=._\-]{20,}`)
	reAuthHeader  = regexp.MustCompile(`(?i)(authorization\s*:\s*)\S+`)
	reJWT         = regexp.MustCompile(`eyJ[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+`)
	reKeyVal      = regexp.MustCompile(`(?i)(["']?(?:api_?key|apikey|token|password|secret)["']?\s*[:=]\s*["']?)[A-Za-z0-9+/=._\-]{8,}(["']?)`)
	reLongHex     = regexp.MustCompile(`\b[0-9a-fA-F]{32,}\b`)
)

// redactSecrets removes tokens, keys, and other sensitive values from s.
func redactSecrets(s string) string {
	if s == "" {
		return s
	}
	s = reJWT.ReplaceAllString(s, "[JWT_REDACTED]")
	s = reBearerToken.ReplaceAllString(s, "${1}[REDACTED]")
	s = reAuthHeader.ReplaceAllString(s, "${1}[REDACTED]")
	s = reKeyVal.ReplaceAllString(s, "${1}[REDACTED]${2}")
	s = reLongHex.ReplaceAllString(s, "[HEX_REDACTED]")
	return s
}

type feedbackResponseOp struct {
	ObjID string `json:"obj_id"`
	Proc  string `json:"proc"`
}

type feedbackResponse struct {
	RequestProc string          `json:"request_proc"`
	Ops         json.RawMessage `json:"ops"`
}

// parseObjID extracts the obj_id from a Corezoid ops API response.
// It handles both object and array forms of the "ops" field.
func parseObjID(body []byte) (string, error) {
	var resp feedbackResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("invalid response: %v", err)
	}
	if resp.RequestProc != "ok" {
		return "", fmt.Errorf("request failed: request_proc=%q", resp.RequestProc)
	}

	// Try ops as a single object first (as in the example response).
	var single feedbackResponseOp
	if err := json.Unmarshal(resp.Ops, &single); err == nil && single.ObjID != "" {
		return single.ObjID, nil
	}

	// Try ops as an array (standard batch response format).
	var many []feedbackResponseOp
	if err := json.Unmarshal(resp.Ops, &many); err == nil && len(many) > 0 && many[0].ObjID != "" {
		return many[0].ObjID, nil
	}

	return "", fmt.Errorf("obj_id not found in response")
}

// handleSendFeedback submits user feedback to the Corezoid feedback process.
// Must only be called after the user has explicitly confirmed sending (enforced
// by the corezoid-feedback skill — not checked here).
func handleSendFeedback(ctx context.Context, args map[string]interface{}) (string, bool) {
	if os.Getenv("COREZOID_FEEDBACK_DISABLED") != "" {
		return "Feedback is disabled by configuration (COREZOID_FEEDBACK_DISABLED).", false
	}

	problem := optStrArg(args, "problem")
	if problem == "" {
		return "Error: 'problem' is required to submit feedback.", true
	}

	cfg := teleCfg
	if cfg.FeedbackEndpoint == "" {
		cfg = loadTelemetryConfig()
	}

	// Reuse cached installation ID if analytics is running; load from disk otherwise.
	iid := installationID
	if iid == "" {
		iid = loadOrCreateInstallationID()
	}

	ts := time.Now().UTC().Format(time.RFC3339)
	event := FeedbackEvent{
		Ts:                ts,
		Problem:           redactSecrets(problem),
		Expected:          redactSecrets(optStrArg(args, "expected")),
		ProposedSolution:  redactSecrets(optStrArg(args, "proposed_solution")),
		Tool:              redactSecrets(optStrArg(args, "tool")),
		TranscriptExcerpt: redactSecrets(optStrArg(args, "transcript_excerpt")),
		Contact:           redactSecrets(optStrArg(args, "contact")),
		Transport:         analyticsTransport,
		ServerVersion:     mcpServerVersion,
		InstallationID:    iid,
	}

	op := map[string]interface{}{
		"type":    "create",
		"obj":     "task",
		"conv_id": cfg.FeedbackConvID,
		"ref":     fmt.Sprintf("%s-%s", iid, ts),
		"data":    event,
	}
	payload := map[string]interface{}{"ops": []interface{}{op}}

	data, err := json.Marshal(payload)
	if err != nil {
		return "Error: failed to encode feedback.", true
	}

	reqCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, cfg.FeedbackEndpoint, bytes.NewReader(data))
	if err != nil {
		return "Error: failed to prepare feedback request.", true
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "Feedback could not be sent (network error). Please try again later.", true
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "Feedback could not be sent (read error). Please try again later.", true
	}

	objID, err := parseObjID(body)
	if err != nil {
		logger.Debug("feedback: parse error: %v, body: %s", err, body)
		return "Feedback could not be submitted (unexpected response). Please try again later.", true
	}

	return fmt.Sprintf("Feedback submitted. Ticket id: %s", objID), false
}
