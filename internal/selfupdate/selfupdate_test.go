package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeRelease returns a minimal Release JSON body for httptest stubs.
func fakeReleaseJSON(tag string, assetNames ...string) string {
	var assets []string
	for _, name := range assetNames {
		assets = append(assets, fmt.Sprintf(
			`{"name":"%s","browser_download_url":"https://example.test/%s","size":42}`,
			name, name,
		))
	}
	return fmt.Sprintf(
		`{"tag_name":"%s","name":"%s","html_url":"https://example.test/release","assets":[%s]}`,
		tag, tag, strings.Join(assets, ","),
	)
}

func TestFetchRelease_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept header = %q, want application/vnd.github+json", got)
		}
		if got := r.Header.Get("User-Agent"); got != "praxis-cli" {
			t.Errorf("User-Agent = %q, want praxis-cli", got)
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(fakeReleaseJSON("v1.2.3", "praxis_darwin_arm64", "checksums.txt")))
	}))
	defer srv.Close()

	rel, err := fetchRelease(srv.URL)
	if err != nil {
		t.Fatalf("fetchRelease err = %v", err)
	}
	if rel.TagName != "v1.2.3" {
		t.Errorf("TagName = %q, want v1.2.3", rel.TagName)
	}
	if len(rel.Assets) != 2 {
		t.Errorf("len(Assets) = %d, want 2", len(rel.Assets))
	}
}

func TestFetchRelease_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	_, err := fetchRelease(srv.URL)
	if err == nil {
		t.Fatal("expected error on 404, got nil")
	}
	if !strings.Contains(err.Error(), "no releases") {
		t.Errorf("err = %v, want substring 'no releases'", err)
	}
}

func TestFetchRelease_500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	_, err := fetchRelease(srv.URL)
	if err == nil {
		t.Fatal("expected error on 500, got nil")
	}
	if !strings.Contains(err.Error(), "github API returned") {
		t.Errorf("err = %v, want substring 'github API returned'", err)
	}
}

func TestFetchRelease_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("{ this isn't json"))
	}))
	defer srv.Close()

	if _, err := fetchRelease(srv.URL); err == nil {
		t.Fatal("expected JSON decode error, got nil")
	}
}

func TestLatestReleaseURL(t *testing.T) {
	got := latestReleaseURL()
	want := "https://api.github.com/repos/Facets-cloud/praxis-cli/releases/latest"
	if got != want {
		t.Errorf("latestReleaseURL() = %q, want %q", got, want)
	}
}

func TestAssetForPlatform_FindsBoth(t *testing.T) {
	suffix := fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)
	rel := &Release{
		TagName: "v1.0.0",
		Assets: []Asset{
			{Name: "praxis_" + suffix, BrowserDownloadURL: "https://example.test/binary"},
			{Name: "checksums.txt", BrowserDownloadURL: "https://example.test/checksums.txt"},
			{Name: "praxis_other_other", BrowserDownloadURL: "https://example.test/wrong"},
		},
	}
	bin, sum, err := AssetForPlatform(rel)
	if err != nil {
		t.Fatalf("AssetForPlatform err = %v", err)
	}
	if bin == nil || !strings.Contains(bin.Name, suffix) {
		t.Errorf("binary asset = %+v, want one matching %s", bin, suffix)
	}
	if sum == nil || sum.Name != "checksums.txt" {
		t.Errorf("checksums asset = %+v, want checksums.txt", sum)
	}
}

func TestAssetForPlatform_NoChecksums(t *testing.T) {
	suffix := fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)
	rel := &Release{
		TagName: "v1.0.0",
		Assets: []Asset{
			{Name: "praxis_" + suffix},
		},
	}
	bin, sum, err := AssetForPlatform(rel)
	if err != nil {
		t.Fatalf("AssetForPlatform err = %v", err)
	}
	if bin == nil {
		t.Errorf("binary asset = nil, want non-nil")
	}
	if sum != nil {
		t.Errorf("checksums asset = %+v, want nil (no checksums.txt in release)", sum)
	}
}

func TestAssetForPlatform_NoMatch(t *testing.T) {
	rel := &Release{
		TagName: "v1.0.0",
		Assets:  []Asset{{Name: "something_else"}},
	}
	_, _, err := AssetForPlatform(rel)
	if err == nil {
		t.Fatal("expected error when no asset matches platform, got nil")
	}
	if !strings.Contains(err.Error(), runtime.GOOS) {
		t.Errorf("err = %v, want it to mention OS %s", err, runtime.GOOS)
	}
}

func TestParseChecksums(t *testing.T) {
	body := `abc123  praxis_linux_amd64
def456  praxis_darwin_arm64
fff999  another_file.txt
`
	tests := []struct {
		name      string
		assetName string
		wantHex   string
		wantErr   bool
	}{
		{"linux", "praxis_linux_amd64", "abc123", false},
		{"darwin", "praxis_darwin_arm64", "def456", false},
		{"missing", "praxis_unknown", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseChecksums(body, tt.assetName)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if got != tt.wantHex {
				t.Errorf("ParseChecksums = %q, want %q", got, tt.wantHex)
			}
		})
	}
}

func TestParseChecksums_EmptyBody(t *testing.T) {
	if _, err := ParseChecksums("", "anything"); err == nil {
		t.Fatal("expected error on empty body, got nil")
	}
}

func TestVerifyChecksum_Match(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "data")
	content := []byte("praxis test fixture")
	if err := os.WriteFile(f, content, 0644); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(content)
	want := hex.EncodeToString(h[:])

	if err := VerifyChecksum(f, want); err != nil {
		t.Errorf("VerifyChecksum err = %v, want nil", err)
	}
}

func TestVerifyChecksum_Mismatch(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "data")
	if err := os.WriteFile(f, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	err := VerifyChecksum(f, "0000000000000000000000000000000000000000000000000000000000000000")
	if err == nil {
		t.Fatal("expected error on checksum mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "mismatch") {
		t.Errorf("err = %v, want substring 'mismatch'", err)
	}
}

func TestVerifyChecksum_FileMissing(t *testing.T) {
	if err := VerifyChecksum("/nonexistent/path/zzz", "deadbeef"); err == nil {
		t.Fatal("expected error on missing file, got nil")
	}
}

func TestVerifyChecksum_TrimmedHex(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "data")
	content := []byte("trim test")
	_ = os.WriteFile(f, content, 0644)
	h := sha256.Sum256(content)
	hexStr := hex.EncodeToString(h[:])

	// Whitespace around the expected hex should be tolerated (mimics ParseChecksums output edge cases).
	if err := VerifyChecksum(f, "  "+hexStr+"\n"); err != nil {
		t.Errorf("VerifyChecksum should tolerate surrounding whitespace, got %v", err)
	}
}

func TestDownload_Success(t *testing.T) {
	payload := []byte("fake binary content")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	path, err := Download(srv.URL)
	if err != nil {
		t.Fatalf("Download err = %v", err)
	}
	defer os.Remove(path)

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("downloaded content = %q, want %q", got, payload)
	}
}

func TestDownload_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	if _, err := Download(srv.URL); err == nil {
		t.Fatal("expected error on 404, got nil")
	}
}

func TestFetchText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("hello world"))
	}))
	defer srv.Close()

	got, err := FetchText(srv.URL)
	if err != nil {
		t.Fatalf("FetchText err = %v", err)
	}
	if got != "hello world" {
		t.Errorf("FetchText = %q, want %q", got, "hello world")
	}
}

func TestFetchText_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	if _, err := FetchText(srv.URL); err == nil {
		t.Fatal("expected error on 500, got nil")
	}
}

func TestAtomicReplace(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "binary")
	newFile := filepath.Join(dir, "downloaded")

	if err := os.WriteFile(current, []byte("OLD"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newFile, []byte("NEW"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := AtomicReplace(current, newFile); err != nil {
		t.Fatalf("AtomicReplace err = %v", err)
	}

	got, err := os.ReadFile(current)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "NEW" {
		t.Errorf("after replace, content = %q, want NEW", got)
	}

	// Permissions should match the original (0700)
	info, err := os.Stat(current)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0700 {
		t.Errorf("perm = %o, want 0700", mode)
	}

	// Source temp file should be gone (renamed away)
	if _, err := os.Stat(newFile); !os.IsNotExist(err) {
		t.Errorf("temp file still exists after rename: %v", err)
	}
}

func TestAtomicReplace_TargetMissing_DefaultsTo0755(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "doesnotexist")
	newFile := filepath.Join(dir, "downloaded")
	if err := os.WriteFile(newFile, []byte("X"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := AtomicReplace(current, newFile); err != nil {
		t.Fatalf("AtomicReplace err = %v", err)
	}
	info, err := os.Stat(current)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0755 {
		t.Errorf("perm = %o, want 0755 (fallback when current missing)", mode)
	}
}
