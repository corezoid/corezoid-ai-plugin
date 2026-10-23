package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDiscoverOAuthMetadata(t *testing.T) {
	var issuer string
	var supportIss bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-authorization-server" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		iss := "false"
		if supportIss {
			iss = "true"
		}
		_, _ = w.Write([]byte(`{"issuer":"` + issuer + `","authorization_endpoint":"` + issuer + `/auth","token_endpoint":"` + issuer + `/tok","authorization_response_iss_parameter_supported":` + iss + `}`))
	}))
	defer ts.Close()

	issuer, supportIss = ts.URL, true
	md, err := discoverOAuthMetadata(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	if md.AuthorizationEndpoint != ts.URL+"/auth" || md.TokenEndpoint != ts.URL+"/tok" || !md.AuthorizationResponseIssParamSupport {
		t.Errorf("metadata = %+v", md)
	}

	// A metadata document naming another issuer is rejected outright.
	issuer = "https://evil.example"
	if _, err := discoverOAuthMetadata(ts.URL); err == nil {
		t.Error("expected issuer mismatch error")
	}
}

func TestDiscoverOAuthMetadata_FallbackWhenAbsent(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	defer ts.Close()
	md, err := discoverOAuthMetadata(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	if md.Issuer != ts.URL || md.AuthorizationEndpoint != ts.URL+"/oauth2/authorize" || md.TokenEndpoint != ts.URL+"/oauth2/token" {
		t.Errorf("fallback = %+v", md)
	}
}

func TestCheckAuthorizationResponseIssuer(t *testing.T) {
	md := oauthServerMetadata{Issuer: "https://account.corezoid.com"}
	if err := checkAuthorizationResponseIssuer(md, ""); err != nil {
		t.Errorf("absent iss without server support: %v", err)
	}
	if err := checkAuthorizationResponseIssuer(md, "https://account.corezoid.com/"); err != nil {
		t.Errorf("matching iss: %v", err)
	}
	if err := checkAuthorizationResponseIssuer(md, "https://attacker.example"); err == nil {
		t.Error("mismatched iss accepted")
	}
	md.AuthorizationResponseIssParamSupport = true
	if err := checkAuthorizationResponseIssuer(md, ""); err == nil {
		t.Error("missing iss accepted although the server advertises it")
	}
}

func TestIsClientIDMetadataDocumentURL(t *testing.T) {
	for id, want := range map[string]bool{
		oauthDefaultClientID:                       false,
		"https://plugin.example/oauth/client.json": true,
		"http://plugin.example/oauth/client.json":  false,
		"https://plugin.example":                   false,
	} {
		if got := isClientIDMetadataDocumentURL(id); got != want {
			t.Errorf("%q: got %v want %v", id, got, want)
		}
	}
}

// TestSyncGlobals_TokenBoundToIssuer: a saved token is only used against the
// account that issued it.
func TestSyncGlobals_TokenBoundToIssuer(t *testing.T) {
	t.Cleanup(func() { syncGlobalsFromFolder(nil) })
	for _, k := range []string{"COREZOID_ACCESS_TOKEN", "COREZOID_ACCOUNT_URL", "COREZOID_TOKEN_EXPIRES_AT"} {
		t.Setenv(k, "")
	}
	f := &Folder{AccountURL: "https://account.corezoid.com", AccessToken: "tok", ExpiresAt: time.Now().Add(time.Hour), Issuer: "https://account.corezoid.com/"}
	syncGlobalsFromFolder(f)
	if _, tok, _, _, _ := authSnapshot(); tok != "tok" {
		t.Errorf("token for its own issuer dropped: %q", tok)
	}

	f.AccountURL = "https://account.other.example"
	syncGlobalsFromFolder(f)
	if _, tok, _, _, _ := authSnapshot(); tok != "" {
		t.Error("token bound to another issuer was used")
	}

	// Credentials saved before issuer binding (no Issuer) keep working.
	f.Issuer = ""
	syncGlobalsFromFolder(f)
	if _, tok, _, _, _ := authSnapshot(); tok != "tok" {
		t.Errorf("legacy unbound token dropped: %q", tok)
	}
}
