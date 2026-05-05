// Package selfupdate fetches the latest praxis release from GitHub, verifies
// its checksum, and atomically replaces the running binary.
//
// The release-asset naming convention assumed here is goreleaser's default:
// `praxis_<os>_<arch>` (raw binary). If goreleaser is configured to ship
// archives instead, AssetForPlatform will need an extraction step.
package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

const (
	repoOwner = "Facets-cloud"
	repoName  = "praxis-cli"
)

// Release is the subset of GitHub's release JSON we use.
type Release struct {
	TagName string  `json:"tag_name"`
	Name    string  `json:"name"`
	HTMLURL string  `json:"html_url"`
	Assets  []Asset `json:"assets"`
}

// Asset is one release artifact (binary, checksums file, …).
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// LatestRelease returns metadata for the most recent published release.
func LatestRelease() (*Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", repoOwner, repoName)
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "praxis-cli")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("no releases published yet")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("github API returned %s", resp.Status)
	}
	var r Release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

// AssetForPlatform finds the release asset matching the running OS/arch and
// the corresponding checksums.txt asset (nil if the release lacks one).
func AssetForPlatform(r *Release) (binary *Asset, checksums *Asset, err error) {
	suffix := fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)
	for i := range r.Assets {
		a := &r.Assets[i]
		lname := strings.ToLower(a.Name)
		if strings.Contains(lname, suffix) {
			binary = a
		}
		if a.Name == "checksums.txt" {
			checksums = a
		}
	}
	if binary == nil {
		return nil, nil, fmt.Errorf(
			"release %s has no asset for %s/%s",
			r.TagName, runtime.GOOS, runtime.GOARCH,
		)
	}
	return binary, checksums, nil
}

// Download fetches the URL into a temp file and returns its path.
func Download(url string) (string, error) {
	client := &http.Client{Timeout: 5 * time.Minute}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "praxis-cli")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download returned %s", resp.Status)
	}
	tmp, err := os.CreateTemp("", "praxis-update-*")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	return tmp.Name(), nil
}

// FetchText downloads a small text body (capped at 1 MB) and returns it.
func FetchText(url string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "praxis-cli")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("fetch %s returned %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return string(b), err
}

// VerifyChecksum compares the SHA256 of file at path against expected hex.
func VerifyChecksum(path, expectedHex string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != strings.TrimSpace(expectedHex) {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedHex, got)
	}
	return nil
}

// ParseChecksums reads a goreleaser-style checksums.txt body and returns the
// hex digest for the named asset. Format per line: "<hex>  <filename>".
func ParseChecksums(body, assetName string) (string, error) {
	for _, line := range strings.Split(body, "\n") {
		parts := strings.Fields(line)
		if len(parts) >= 2 && parts[1] == assetName {
			return parts[0], nil
		}
	}
	return "", fmt.Errorf("checksum for %s not found in checksums.txt", assetName)
}

// AtomicReplace replaces the binary at currentPath with the file at newPath.
// On Linux/macOS, os.Rename is atomic and safe to use against a running
// executable: the kernel maps exec'd images, so the on-disk file can be
// replaced without disturbing the running process.
func AtomicReplace(currentPath, newPath string) error {
	if info, err := os.Stat(currentPath); err == nil {
		if err := os.Chmod(newPath, info.Mode()); err != nil {
			return fmt.Errorf("chmod new binary: %w", err)
		}
	} else if err := os.Chmod(newPath, 0755); err != nil {
		return fmt.Errorf("chmod new binary: %w", err)
	}
	if err := os.Rename(newPath, currentPath); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}
