package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// req sends an authenticated JSON-RPC request to the Corezoid API.
func (v *Executor) req(method string, ops []map[string]any) (map[string]interface{}, error) {
	// Personal-workspace accounts have no companyID. The callers in this file
	// unconditionally inject `"company_id": workspaceID` into every op, so when
	// workspaceID is empty the payload carries `"company_id": ""` and Corezoid
	// rejects every request with `Value is not valid / company_id`. Drop the
	// empty placeholder (and its mirrors) so the request matches what the API
	// accepts for personal accounts. No-op for normal company workspaces.
	if strings.TrimSpace(workspaceID) == "" {
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
		json.Indent(&prettyJSON, payloadJSON, "", "  ")
		logger.Debug("API Request, method=%s, payload=%s", method, prettyJSON.String())
	}

	path := "json"
	if method == "export_process" {
		path = "download"
	}
	authURL := fmt.Sprintf("%s/api/2/%s", v.APIUrl, path)

	if v.Debug {
		logger.Debug("Request URL, url=%s", authURL)
	}

	req, err := http.NewRequest("POST", authURL, bytes.NewBuffer(payloadJSON))
	if err != nil {
		logger.Error("Error creating request: %v", err)
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Simulator %s", v.Token))
	logger.Debug("Header: %v", req.Header)

	client := newHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		logger.Error("Error making request: %v", err)
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("Error reading response: %v", err)
		return nil, err
	}

	if v.Debug {
		var prettyJSON bytes.Buffer
		if json.Indent(&prettyJSON, body, "", "  ") == nil {
			logger.Debug("API Response, method=%s, status=%s, body=%s", method, resp.Status, prettyJSON.String())
		} else {
			logger.Debug("API Response, method=%s, status=%s, body=%s", method, resp.Status, string(body))
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

func (v *Executor) checkError(rsp map[string]interface{}) error {
	if rsp == nil {
		return fmt.Errorf("failed to compile API code: no response from server")
	}

	if requestProc, ok := rsp["request_proc"].(string); !ok || requestProc != "ok" {
		return fmt.Errorf("failed to compile API code: request_proc not ok")
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
							return fmt.Errorf("failed to compile API code:\n%s", strings.Join(errorMsgs, "\n"))
						}
					}

					errorMsg := "unknown error"
					if msg, ok := opMap["description"].(string); ok {
						errorMsg = msg
					}
					return fmt.Errorf("failed to execute: %s", errorMsg)
				}
			}
		}
	}
	return nil
}
