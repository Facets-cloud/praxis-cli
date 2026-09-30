package raptorinstall

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureDownloadValidation(t *testing.T) {
	for _, tc := range []struct {
		name, digest, body string
		size               int
		status             int
		want               string
	}{
		{"success", "", "binary", 6, 200, ""},
		{"wrong-checksum", "sha256:" + strings.Repeat("0", 64), "binary", 6, 200, "verification"},
		{"missing-checksum", "absent", "binary", 6, 200, "digest"},
		{"truncated", "", "bin", 6, 200, "verification"},
		{"oversized", "", "binary-too-long", 6, 200, "verification"},
		{"bad-size", "", "binary", 0, 200, "size"},
		{"download-failed", "", "binary", 6, 502, "HTTP 502"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("PATH", t.TempDir())
			digest := tc.digest
			if digest == "" {
				digest = fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("binary")))
			}
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/release" {
					fmt.Fprintf(w, `{"assets":[{"name":"raptor-linux-arm64","size":%d,"digest":%q,"browser_download_url":%q}]}`, tc.size, digest, server.URL+"/binary")
					return
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			result, err := ensure(server.Client(), server.URL+"/release", "linux", "arm64")
			dest := filepath.Join(home, ".local/bin/raptor")
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("error=%v, want %s", err, tc.want)
				}
				if _, err := os.Lstat(dest); !os.IsNotExist(err) {
					t.Fatalf("failed download activated: %v", err)
				}
			} else {
				if err != nil || !result.Installed || result.Path != dest || result.PathWarning == "" {
					t.Fatalf("result=%+v err=%v", result, err)
				}
				if body, err := os.ReadFile(dest); err != nil || string(body) != "binary" {
					t.Fatalf("body=%s err=%v", body, err)
				}
			}
			leftovers, _ := filepath.Glob(filepath.Join(home, ".local/bin/.raptor-download-*"))
			if len(leftovers) > 0 {
				t.Fatalf("temporary binaries leaked: %v", leftovers)
			}
		})
	}
}

func TestEnsurePreservesExistingFiles(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mode      os.FileMode
		symlink   bool
		wantError bool
	}{
		{"executable", 0700, false, false}, {"non-executable", 0600, false, true}, {"dangling-link", 0, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("PATH", t.TempDir())
			dest := filepath.Join(home, ".local/bin/raptor")
			os.MkdirAll(filepath.Dir(dest), 0700)
			if tc.symlink {
				os.Symlink("missing-source", dest)
			} else {
				os.WriteFile(dest, []byte("keep-me"), tc.mode)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("existing path must not download") }))
			defer server.Close()
			result, err := ensure(server.Client(), server.URL, "linux", "amd64")
			if (err != nil) != tc.wantError || result.Installed {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if tc.symlink {
				if link, _ := os.Readlink(dest); link != "missing-source" {
					t.Fatal("symlink changed")
				}
			} else if body, _ := os.ReadFile(dest); string(body) != "keep-me" {
				t.Fatal("existing file changed")
			}
		})
	}
}

func TestEnsureUnsupportedAndMalformedRelease(t *testing.T) {
	for _, tc := range []struct {
		name, goos, body, want string
		status                 int
	}{
		{"unsupported", "windows", `{}`, "supported", 200},
		{"malformed", "linux", `{`, "metadata", 200},
		{"missing-asset", "linux", `{"assets":[]}`, "no asset", 200},
		{"metadata-unavailable", "linux", `{}`, "HTTP 403", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("PATH", t.TempDir())
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) }))
			defer server.Close()
			_, err := ensure(server.Client(), server.URL, tc.goos, "amd64")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestEnsureDoesNotClobberConcurrentInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	dest := filepath.Join(home, ".local/bin/raptor")
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/release" {
			fmt.Fprintf(w, `{"assets":[{"name":"raptor-linux-amd64","size":6,"digest":"sha256:%x","browser_download_url":%q}]}`, sha256.Sum256([]byte("binary")), server.URL+"/binary")
			return
		}
		os.MkdirAll(filepath.Dir(dest), 0700)
		os.WriteFile(dest, []byte("other-install"), 0700)
		fmt.Fprint(w, "binary")
	}))
	defer server.Close()
	_, err := ensure(server.Client(), server.URL+"/release", "linux", "amd64")
	if err == nil || !strings.Contains(err.Error(), "without overwrite") {
		t.Fatalf("err=%v", err)
	}
	if body, _ := os.ReadFile(dest); string(body) != "other-install" {
		t.Fatal("concurrent install overwritten")
	}
}

func TestEnsureRejectsRelativePATHExecutable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	base := t.TempDir()
	t.Chdir(base)
	os.Mkdir("bin", 0700)
	os.WriteFile("bin/raptor", []byte("#!/bin/sh\n"), 0700)
	t.Setenv("PATH", "bin")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("ambiguous PATH must not trigger a replacement download")
	}))
	defer server.Close()
	_, err := ensure(server.Client(), server.URL, "linux", "amd64")
	if !errors.Is(err, exec.ErrDot) {
		t.Fatalf("must preserve Go's untrusted relative PATH guard, got %v", err)
	}
}
