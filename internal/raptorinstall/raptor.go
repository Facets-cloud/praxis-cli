// Package raptorinstall locates and bootstraps the independently released Raptor CLI.
package raptorinstall

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type Result struct {
	Path        string `json:"path,omitempty"`
	Installed   bool   `json:"installed_now"`
	PathWarning string `json:"path_warning,omitempty"`
}

// Ensure never upgrades or overwrites an existing binary, and requires no sudo.
func Ensure() (Result, error) {
	return ensure(&http.Client{Timeout: 45 * time.Second}, "https://api.github.com/repos/Facets-cloud/raptor-releases/releases/latest", runtime.GOOS, runtime.GOARCH)
}

func ensure(client *http.Client, endpoint, goos, goarch string) (Result, error) {
	if path, err := Find(); err == nil {
		return resultFor(path, false), nil
	} else if errors.Is(err, exec.ErrDot) {
		return Result{}, err
	}
	if (goos != "darwin" && goos != "linux") || (goarch != "amd64" && goarch != "arm64") {
		return Result{}, fmt.Errorf("Raptor has no supported binary for %s/%s", goos, goarch)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Result{}, err
	}
	dest := filepath.Join(home, ".local", "bin", "raptor")
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		return Result{}, fmt.Errorf("preserving existing non-executable Raptor path %s; repair it explicitly", dest)
	}
	response, err := get(client, endpoint)
	if err != nil {
		return Result{}, err
	}
	var release struct {
		Assets []struct {
			Name, Digest string
			Size         int64
			URL          string `json:"browser_download_url"`
		}
	}
	err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&release)
	response.Body.Close()
	if err != nil {
		return Result{}, fmt.Errorf("Raptor release metadata: %w", err)
	}
	name := "raptor-" + goos + "-" + goarch
	for _, asset := range release.Assets {
		if asset.Name != name {
			continue
		}
		want, err := hex.DecodeString(strings.TrimPrefix(asset.Digest, "sha256:"))
		if !strings.HasPrefix(asset.Digest, "sha256:") || err != nil || len(want) != sha256.Size {
			return Result{}, fmt.Errorf("Raptor release lacks a valid SHA-256 digest for %s", name)
		}
		const limit = 128 << 20
		if asset.Size <= 0 || asset.Size > limit {
			return Result{}, fmt.Errorf("invalid Raptor binary size: %d", asset.Size)
		}
		response, err := get(client, asset.URL)
		if err != nil {
			return Result{}, err
		}
		defer response.Body.Close()
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return Result{}, err
		}
		file, err := os.CreateTemp(filepath.Dir(dest), ".raptor-download-*")
		if err != nil {
			return Result{}, err
		}
		defer os.Remove(file.Name())
		defer file.Close()
		hash := sha256.New()
		n, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, asset.Size+1))
		if err != nil {
			return Result{}, fmt.Errorf("Raptor download: %w", err)
		}
		if n != asset.Size || hex.EncodeToString(hash.Sum(nil)) != hex.EncodeToString(want) {
			return Result{}, fmt.Errorf("Raptor download failed size/SHA-256 verification")
		}
		if err := file.Chmod(0755); err != nil {
			return Result{}, err
		}
		if err := file.Close(); err != nil {
			return Result{}, err
		}
		// Link activates atomically without clobbering a concurrent install.
		if err := os.Link(file.Name(), dest); err != nil {
			return Result{}, fmt.Errorf("activate Raptor without overwrite: %w", err)
		}
		return resultFor(dest, true), nil
	}
	return Result{}, fmt.Errorf("Raptor release has no asset %s", name)
}

func get(client *http.Client, url string) (*http.Response, error) {
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "praxis-cli")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("Raptor download returned HTTP %d", response.StatusCode)
	}
	return response, nil
}

func resultFor(path string, installed bool) Result {
	result := Result{Path: path, Installed: installed}
	if _, err := exec.LookPath("raptor"); err != nil {
		result.PathWarning = "Add " + filepath.Dir(path) + " to PATH to run raptor from your shell; Praxis can use the installed absolute path."
	}
	return result
}

// Find prefers PATH, then the user-local install even before the shell PATH is updated.
func Find() (string, error) {
	if path, err := exec.LookPath("raptor"); err == nil {
		return path, nil
	} else if errors.Is(err, exec.ErrDot) {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return exec.LookPath(filepath.Join(home, ".local", "bin", "raptor"))
}
