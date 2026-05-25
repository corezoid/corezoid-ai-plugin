package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUpdateEnvFile_NewKey(t *testing.T) {
	f := filepath.Join(t.TempDir(), ".env")
	if err := updateEnvFile(f, "FOO", "bar"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := os.ReadFile(f)
	if string(data) != "FOO=bar\n" {
		t.Errorf("unexpected content: %q", string(data))
	}
}

func TestUpdateEnvFile_UpdateExistingKey(t *testing.T) {
	f := filepath.Join(t.TempDir(), ".env")
	_ = os.WriteFile(f, []byte("FOO=old\nBAR=keep\n"), 0600)
	if err := updateEnvFile(f, "FOO", "new"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := os.ReadFile(f)
	if string(data) != "FOO=new\nBAR=keep\n" {
		t.Errorf("unexpected content: %q", string(data))
	}
}

func TestUpdateEnvFile_MultipleKeys(t *testing.T) {
	f := filepath.Join(t.TempDir(), ".env")
	_ = updateEnvFile(f, "A", "1")
	_ = updateEnvFile(f, "B", "2")
	_ = updateEnvFile(f, "A", "updated")
	data, _ := os.ReadFile(f)
	content := string(data)
	if content != "A=updated\nB=2\n" {
		t.Errorf("unexpected content: %q", content)
	}
}

func TestRemoveEnvKey_ExistingKey(t *testing.T) {
	f := filepath.Join(t.TempDir(), ".env")
	_ = os.WriteFile(f, []byte("FOO=bar\nBAZ=qux\n"), 0600)
	if err := removeEnvKey(f, "FOO"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := os.ReadFile(f)
	if string(data) != "BAZ=qux\n" {
		t.Errorf("unexpected content: %q", string(data))
	}
}

func TestRemoveEnvKey_MissingFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "nonexistent.env")
	if err := removeEnvKey(f, "FOO"); err != nil {
		t.Errorf("expected nil for missing file, got: %v", err)
	}
}

func TestRemoveEnvKey_LastKey(t *testing.T) {
	f := filepath.Join(t.TempDir(), ".env")
	_ = os.WriteFile(f, []byte("ONLY=val\n"), 0600)
	_ = removeEnvKey(f, "ONLY")
	data, _ := os.ReadFile(f)
	if string(data) != "" {
		t.Errorf("expected empty file after removing last key, got: %q", string(data))
	}
}

func TestLoadCredentials_NoToken(t *testing.T) {
	t.Setenv("ACCESS_TOKEN", "")
	creds, err := loadCredentials()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds != nil {
		t.Errorf("expected nil credentials when token absent, got: %+v", creds)
	}
}

func TestLoadCredentials_WithToken(t *testing.T) {
	t.Setenv("ACCESS_TOKEN", "tok123")
	t.Setenv("ACCESS_TOKEN_EXPIRES_AT", "")
	creds, err := loadCredentials()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds == nil || creds.AccessToken != "tok123" {
		t.Errorf("expected token tok123, got: %+v", creds)
	}
}

func TestLoadCredentials_WithExpiry(t *testing.T) {
	expiry := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	t.Setenv("ACCESS_TOKEN", "tok456")
	t.Setenv("ACCESS_TOKEN_EXPIRES_AT", expiry)
	creds, err := loadCredentials()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds.ExpiresAt.IsZero() {
		t.Error("expected non-zero ExpiresAt")
	}
}

func TestIsCredentialsExpired_ZeroTime(t *testing.T) {
	creds := &Credentials{AccessToken: "tok"}
	if isCredentialsExpired(creds) {
		t.Error("zero ExpiresAt should not be considered expired")
	}
}

func TestIsCredentialsExpired_Future(t *testing.T) {
	creds := &Credentials{AccessToken: "tok", ExpiresAt: time.Now().Add(time.Hour)}
	if isCredentialsExpired(creds) {
		t.Error("future expiry should not be expired")
	}
}

func TestIsCredentialsExpired_Past(t *testing.T) {
	creds := &Credentials{AccessToken: "tok", ExpiresAt: time.Now().Add(-time.Hour)}
	if !isCredentialsExpired(creds) {
		t.Error("past expiry should be expired")
	}
}

func TestSaveAndDeleteCredentials(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("COREZOID_WORK_DIR", dir)
	// Point envFilePath to the temp dir by changing workdir for this test.
	orig, _ := os.Getwd()
	defer func() { _ = os.Chdir(orig) }()
	_ = os.Chdir(dir)

	creds := &Credentials{
		AccessToken: "save_test_token",
		ExpiresAt:   time.Now().Add(time.Hour),
	}
	if err := saveCredentials(creds); err != nil {
		t.Fatalf("saveCredentials error: %v", err)
	}
	if os.Getenv("ACCESS_TOKEN") != "save_test_token" {
		t.Error("ACCESS_TOKEN env var not set after save")
	}

	if err := deleteCredentials(); err != nil {
		t.Fatalf("deleteCredentials error: %v", err)
	}
	if os.Getenv("ACCESS_TOKEN") != "" {
		t.Error("ACCESS_TOKEN should be cleared after delete")
	}
}
