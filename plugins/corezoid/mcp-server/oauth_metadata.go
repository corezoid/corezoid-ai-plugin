package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Authorization server metadata (RFC 8414), the authorization response issuer
// check (RFC 9207) and client identification as the MCP 2026-07-28
// authorization spec requires them.
//
// Client identification: the plugin never uses Dynamic Client Registration,
// even though account.corezoid.com advertises a registration_endpoint. By
// default it presents the pre-registered client ID (oauthDefaultClientID).
// When COREZOID_OAUTH_CLIENT_ID is an https URL, it is a Client ID Metadata
// Document URL: the authorization server fetches the client's metadata from
// it, and the plugin sends it as client_id unchanged.

// oauthServerMetadata is the subset of RFC 8414 metadata the PKCE flow uses.
type oauthServerMetadata struct {
	Issuer                               string `json:"issuer"`
	AuthorizationEndpoint                string `json:"authorization_endpoint"`
	TokenEndpoint                        string `json:"token_endpoint"`
	AuthorizationResponseIssParamSupport bool   `json:"authorization_response_iss_parameter_supported"`
	ClientIDMetadataDocumentSupported    bool   `json:"client_id_metadata_document_supported"`
}

// oauthMetadataClient is the HTTP client for the metadata fetch. A var so
// tests can point it at an httptest server.
var oauthMetadataClient = &http.Client{Timeout: 10 * time.Second}

// discoverOAuthMetadata fetches RFC 8414 metadata for accountURL. When the
// server publishes none (or it is unusable), it falls back to the endpoints
// the plugin has always used, with accountURL itself as the expected issuer.
// A metadata document whose issuer is not accountURL is rejected (RFC 8414
// §3.3): it would let one server impersonate another.
func discoverOAuthMetadata(accountURL string) (oauthServerMetadata, error) {
	accountURL = strings.TrimRight(accountURL, "/")
	fallback := oauthServerMetadata{
		Issuer:                accountURL,
		AuthorizationEndpoint: accountURL + "/oauth2/authorize",
		TokenEndpoint:         accountURL + "/oauth2/token",
	}
	u, err := url.Parse(accountURL)
	if err != nil || u.Host == "" {
		return fallback, nil
	}
	metaURL := u.Scheme + "://" + u.Host + "/.well-known/oauth-authorization-server" + strings.TrimRight(u.EscapedPath(), "/")
	req, err := http.NewRequest(http.MethodGet, metaURL, nil)
	if err != nil {
		return fallback, nil
	}
	req.Header.Set("Accept", "application/json")
	resp, err := oauthMetadataClient.Do(req)
	if err != nil {
		logger.Warn("oauth: metadata fetch failed (%v) — using default endpoints", err)
		return fallback, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		logger.Info("oauth: no authorization server metadata at %s (HTTP %d) — using default endpoints", metaURL, resp.StatusCode)
		return fallback, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fallback, nil
	}
	var md oauthServerMetadata
	if err := json.Unmarshal(body, &md); err != nil || md.Issuer == "" {
		logger.Warn("oauth: unusable authorization server metadata at %s — using default endpoints", metaURL)
		return fallback, nil
	}
	if strings.TrimRight(md.Issuer, "/") != accountURL {
		return oauthServerMetadata{}, fmt.Errorf("authorization server metadata issuer %q does not match account URL %q", md.Issuer, accountURL)
	}
	if md.AuthorizationEndpoint == "" {
		md.AuthorizationEndpoint = fallback.AuthorizationEndpoint
	}
	if md.TokenEndpoint == "" {
		md.TokenEndpoint = fallback.TokenEndpoint
	}
	return md, nil
}

// checkAuthorizationResponseIssuer is the RFC 9207 mix-up defence on the
// redirect: when the server says it sends iss, iss is required; when it
// arrives anyway, it must match. got is the iss query parameter ("" if
// absent).
func checkAuthorizationResponseIssuer(md oauthServerMetadata, got string) error {
	if got == "" {
		if md.AuthorizationResponseIssParamSupport {
			return fmt.Errorf("authorization response is missing the iss parameter the server advertises")
		}
		return nil
	}
	if strings.TrimRight(got, "/") != strings.TrimRight(md.Issuer, "/") {
		return fmt.Errorf("authorization response iss %q does not match the expected issuer %q", got, md.Issuer)
	}
	return nil
}

// isClientIDMetadataDocumentURL reports whether clientID is a Client ID
// Metadata Document URL (an https URL with a path) rather than a
// pre-registered client ID.
func isClientIDMetadataDocumentURL(clientID string) bool {
	u, err := url.Parse(clientID)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.Path != "" && u.Path != "/"
}

// sameIssuer compares two issuer identifiers, ignoring a trailing slash.
func sameIssuer(a, b string) bool {
	return strings.TrimRight(a, "/") == strings.TrimRight(b, "/")
}
