// Package mcpmanifest fetches the gateway's /v1/mcp/manifest catalog
// and writes it to ~/.praxis/mcp-tools.json so AI hosts can grep the
// file for available tools without hitting the network on every turn.
//
// The snapshot is best-effort: it's overwritten on `praxis install-skill`
// and `praxis refresh-skills` (i.e. anywhere the catalog is also pulled).
// Callers that want a guaranteed-fresh listing should run `praxis mcp`
// instead, which fetches live.
package mcpmanifest

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/paths"
)

// DefaultTimeout is the HTTP timeout used for the manifest fetch when
// callers don't supply their own.
const DefaultTimeout = 30 * time.Second

// Fetch retrieves the raw manifest JSON from the gateway. Returns the
// raw bytes on HTTP 200, or an error describing the failure mode (so
// the caller can decide whether to soft-skip or hard-fail).
//
// The HTTP transport is exposed as a package var so unit tests can
// inject a stub without standing up a fake server.
var Fetch = func(baseURL, token string, timeout time.Duration) ([]byte, error) {
	if baseURL == "" {
		return nil, errors.New("profile has no URL set")
	}
	if token == "" {
		return nil, errors.New("profile has no token")
	}
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	url := strings.TrimRight(baseURL, "/") + "/ai-api/v1/mcp/manifest"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch manifest: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read manifest body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// Trim very long bodies so error messages stay readable.
		preview := string(raw)
		if len(preview) > 200 {
			preview = preview[:200] + "…"
		}
		return nil, fmt.Errorf("manifest fetch returned HTTP %d: %s", resp.StatusCode, preview)
	}
	return raw, nil
}

// WriteSnapshot writes raw to ~/.praxis/mcp-tools.json atomically (temp
// file + rename) and returns the destination path. The parent dir is
// created if needed.
func WriteSnapshot(raw []byte) (string, error) {
	dest, err := paths.MCPTools()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return dest, fmt.Errorf("ensure dir for %s: %w", dest, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".mcp-tools-*.json")
	if err != nil {
		return dest, fmt.Errorf("create temp file: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return dest, fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return dest, fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		os.Remove(tmp.Name())
		return dest, fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmp.Name(), dest); err != nil {
		os.Remove(tmp.Name())
		return dest, fmt.Errorf("rename to %s: %w", dest, err)
	}
	return dest, nil
}
