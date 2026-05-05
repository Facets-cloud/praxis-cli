package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLogoutCmd_NoCredentials covers the "nothing to remove" branch.
func TestLogoutCmd_NoCredentials(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var buf bytes.Buffer
	logoutCmd.SetOut(&buf)

	if err := logoutCmd.RunE(logoutCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if !strings.Contains(buf.String(), "No credentials to remove") {
		t.Errorf("output = %q, want substring 'No credentials to remove'", buf.String())
	}
}

// TestLogoutCmd_RemovesExisting covers the file-present branch.
func TestLogoutCmd_RemovesExisting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	credPath := filepath.Join(home, ".praxis", "credentials")
	if err := os.MkdirAll(filepath.Dir(credPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credPath, []byte("token-data"), 0600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	logoutCmd.SetOut(&buf)

	if err := logoutCmd.RunE(logoutCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if !strings.Contains(buf.String(), "Removed") {
		t.Errorf("output = %q, want substring 'Removed'", buf.String())
	}
	if _, err := os.Stat(credPath); !os.IsNotExist(err) {
		t.Errorf("credentials file should be gone, stat err = %v", err)
	}
}
