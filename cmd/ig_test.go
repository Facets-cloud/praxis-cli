package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/igcatalog"
)

// resetIgFlags returns the module-level cobra-bound vars to defaults
// between tests — cobra holds these globals across RunE calls.
func resetIgFlags() {
	igJSON = false
	igProfile = ""
	igSyncAll = false
	igPublishCatalog = ""
	igPublishMember = ""
	igPublishGit = ""
	igPublishSha = ""
	igClaimsGit = ""
	igManifestCatalog = ""
	igManifestOut = ""
}

func seedIgProfile(t *testing.T, name string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	if err := credentials.Put(name, credentials.Profile{
		URL: "https://x.test", Username: "u@x.com", Token: "sk_test_T",
	}); err != nil {
		t.Fatal(err)
	}
	if name != "default" {
		if err := credentials.SetActive(name); err != nil {
			t.Fatal(err)
		}
	}
}

func testActive() credentials.Active {
	return credentials.Active{
		Name:    "default",
		Profile: credentials.Profile{URL: "https://x.test", Token: "tok", Username: "u@x.com"},
		Loaded:  true,
	}
}

// makeTarGz builds a gzipped tarball of name→content regular files.
func makeTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha256Etag(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// --- refresh composition (the linchpin) --------------------------------

func TestComposeRefresh_ProfileFlagOnlyForNonDefault(t *testing.T) {
	if got := composeRefresh("payments", "default"); got != "praxis ig sync payments" {
		t.Errorf("default profile: got %q; want no --profile", got)
	}
	if got := composeRefresh("payments", "acme"); got != "praxis ig sync payments --profile acme" {
		t.Errorf("non-default profile: got %q", got)
	}
}

// --- sync: writes .sync.json with all four fields ----------------------

func TestIgSync_WritesSyncStateAndComposesRefresh(t *testing.T) {
	igHomeDir := t.TempDir()
	t.Setenv("IG_HOME", igHomeDir)
	resetIgFlags()
	defer resetIgFlags()

	tarball := makeTarGz(t, map[string]string{
		"metadata.json":         `{"catalog":"payments"}`,
		"graph.json":            `{"root":true}`,
		"member/api/graph.json": `{"m":"api"}`,
	})
	etag := sha256Etag(tarball)

	var gotINM string
	orig := igcatalog.DownloadBundle
	igcatalog.DownloadBundle = func(baseURL, token, catalog, inm string) ([]byte, string, bool, error) {
		gotINM = inm
		if baseURL != "https://x.test" || token != "tok" {
			t.Errorf("auth threading: url=%q token=%q", baseURL, token)
		}
		if catalog != "payments" {
			t.Errorf("catalog = %q", catalog)
		}
		return tarball, etag, false, nil
	}
	defer func() { igcatalog.DownloadBundle = orig }()

	active := credentials.Active{
		Name:    "acme",
		Profile: credentials.Profile{URL: "https://x.test", Token: "tok", Username: "u@x.com"},
		Loaded:  true,
	}
	upToDate, err := syncOne(active, "payments")
	if err != nil {
		t.Fatalf("syncOne: %v", err)
	}
	if upToDate {
		t.Error("first sync should not report up-to-date")
	}
	if gotINM != "" {
		t.Errorf("first sync should send empty If-None-Match, got %q", gotINM)
	}

	dir := filepath.Join(igHomeDir, "projects", "payments")
	if b, err := os.ReadFile(filepath.Join(dir, "graph.json")); err != nil || string(b) != `{"root":true}` {
		t.Errorf("graph.json = %q err=%v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "member", "api", "graph.json")); err != nil || string(b) != `{"m":"api"}` {
		t.Errorf("member graph.json = %q err=%v", b, err)
	}

	st, ok := readSyncState(dir)
	if !ok {
		t.Fatal(".sync.json was not written")
	}
	if st.Digest != etag {
		t.Errorf("digest = %q; want %q", st.Digest, etag)
	}
	if st.From != "praxis@acme:payments@"+etag {
		t.Errorf("from = %q; want praxis@acme:payments@%s", st.From, etag)
	}
	if st.Refresh != "praxis ig sync payments --profile acme" {
		t.Errorf("refresh = %q; want the exact reproducing command with --profile acme", st.Refresh)
	}
	if st.SyncedAt == "" {
		t.Error("synced_at empty")
	}
	if _, e := time.Parse(time.RFC3339, st.SyncedAt); e != nil {
		t.Errorf("synced_at %q not RFC3339: %v", st.SyncedAt, e)
	}
}

// --- sync: 304 keeps the tree, sends If-None-Match ---------------------

func TestIgSync_NotModifiedIsCheapNoOp(t *testing.T) {
	igHomeDir := t.TempDir()
	t.Setenv("IG_HOME", igHomeDir)
	resetIgFlags()
	defer resetIgFlags()

	dir := filepath.Join(igHomeDir, "projects", "payments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("KEEP-ME"), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := syncState{
		SyncedAt: "2026-07-01T00:00:00Z", Digest: "sha256:old",
		From: "praxis@default:payments@sha256:old", Refresh: "praxis ig sync payments",
	}
	if err := writeSyncState(dir, prev); err != nil {
		t.Fatal(err)
	}

	var gotINM string
	orig := igcatalog.DownloadBundle
	igcatalog.DownloadBundle = func(_, _, _, inm string) ([]byte, string, bool, error) {
		gotINM = inm
		return nil, "sha256:old", true, nil
	}
	defer func() { igcatalog.DownloadBundle = orig }()

	upToDate, err := syncOne(testActive(), "payments")
	if err != nil {
		t.Fatalf("syncOne: %v", err)
	}
	if !upToDate {
		t.Error("304 must report up-to-date")
	}
	if gotINM != "sha256:old" {
		t.Errorf("If-None-Match = %q; want the local digest sha256:old", gotINM)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "marker.txt")); err != nil || string(b) != "KEEP-ME" {
		t.Errorf("304 must not re-extract; marker = %q err=%v", b, err)
	}
	st, _ := readSyncState(dir)
	if st.SyncedAt != "2026-07-01T00:00:00Z" {
		t.Errorf("304 must not rewrite .sync.json; got %+v", st)
	}
}

// --- sync: ETag is quoted HTTP syntax, not catalog data (RFC 9110) -----
//
// These two hit a REAL httptest.Server rather than stubbing
// igcatalog.DownloadBundle, so the actual net/http header round-trip is
// exercised end to end: a real server's `ETag` header is a *quoted*
// string (RFC 9110 §8.8.3), e.g. `ETag: "v1.2.3"`. syncOne must store the
// bare tag — no quotes — in .sync.json's digest/from fields, since a
// human reads `from` in ig's provenance footer and quotes there are HTTP
// syntax leaking into displayed data.

func TestIgSync_StripsQuotedETagIntoDigestAndFrom(t *testing.T) {
	igHomeDir := t.TempDir()
	t.Setenv("IG_HOME", igHomeDir)
	resetIgFlags()
	defer resetIgFlags()

	tarball := makeTarGz(t, map[string]string{
		"metadata.json": `{"catalog":"payments"}`,
		"graph.json":    `{"root":true}`,
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1.2.3"`)
		_, _ = w.Write(tarball)
	}))
	defer srv.Close()

	active := credentials.Active{
		Name:    "acme",
		Profile: credentials.Profile{URL: srv.URL, Token: "tok", Username: "u@x.com"},
		Loaded:  true,
	}
	if _, err := syncOne(active, "payments"); err != nil {
		t.Fatalf("syncOne: %v", err)
	}

	dir := filepath.Join(igHomeDir, "projects", "payments")
	st, ok := readSyncState(dir)
	if !ok {
		t.Fatal(".sync.json was not written")
	}
	if st.Digest != "v1.2.3" {
		t.Errorf("digest = %q; want the bare tag v1.2.3 with no quotes", st.Digest)
	}
	if st.From != "praxis@acme:payments@v1.2.3" {
		t.Errorf("from = %q; want praxis@acme:payments@v1.2.3 with no quotes", st.From)
	}
}

// TestIgSync_ReSyncSendsQuotedIfNoneMatchAndTreats304AsUpToDate is the
// send-side half: once the stored digest is bare (post-fix), re-syncing
// must re-quote it before sending If-None-Match — a bare unquoted token
// is invalid HTTP syntax and a spec-compliant server need not honor it —
// and a resulting 304 must still be treated as "already current" with no
// re-extract.
func TestIgSync_ReSyncSendsQuotedIfNoneMatchAndTreats304AsUpToDate(t *testing.T) {
	igHomeDir := t.TempDir()
	t.Setenv("IG_HOME", igHomeDir)
	resetIgFlags()
	defer resetIgFlags()

	dir := filepath.Join(igHomeDir, "projects", "payments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("KEEP-ME"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A catalog already synced by the FIXED code: the stored digest is
	// bare, no quotes.
	if err := writeSyncState(dir, syncState{
		SyncedAt: "2026-07-01T00:00:00Z", Digest: "v1.2.3",
		From: "praxis@default:payments@v1.2.3", Refresh: "praxis ig sync payments",
	}); err != nil {
		t.Fatal(err)
	}

	var gotINM string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotINM = r.Header.Get("If-None-Match")
		w.Header().Set("ETag", `"v1.2.3"`)
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	active := credentials.Active{
		Name:    "default",
		Profile: credentials.Profile{URL: srv.URL, Token: "tok", Username: "u@x.com"},
		Loaded:  true,
	}
	upToDate, err := syncOne(active, "payments")
	if err != nil {
		t.Fatalf("syncOne: %v", err)
	}
	if !upToDate {
		t.Error("304 must report up-to-date")
	}
	if gotINM != `"v1.2.3"` {
		t.Errorf("If-None-Match sent = %q; want the properly quoted validator %q", gotINM, `"v1.2.3"`)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "marker.txt")); err != nil || string(b) != "KEEP-ME" {
		t.Errorf("304 must not re-extract; marker = %q err=%v", b, err)
	}
	st, _ := readSyncState(dir)
	if st.SyncedAt != "2026-07-01T00:00:00Z" {
		t.Errorf("304 must not rewrite .sync.json; got %+v", st)
	}
}

// --- sync: digest mismatch fails loudly, tree intact -------------------

func TestIgSync_DigestMismatchFailsAndKeepsTree(t *testing.T) {
	igHomeDir := t.TempDir()
	t.Setenv("IG_HOME", igHomeDir)
	resetIgFlags()
	defer resetIgFlags()

	dir := filepath.Join(igHomeDir, "projects", "payments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("KEEP-ME"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeSyncState(dir, syncState{Digest: "sha256:old"}); err != nil {
		t.Fatal(err)
	}

	tarball := makeTarGz(t, map[string]string{"graph.json": "x"})
	orig := igcatalog.DownloadBundle
	igcatalog.DownloadBundle = func(_, _, _, _ string) ([]byte, string, bool, error) {
		return tarball, "sha256:0000thisisthewronghash0000", false, nil
	}
	defer func() { igcatalog.DownloadBundle = orig }()

	if _, err := syncOne(testActive(), "payments"); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("expected digest mismatch error, got %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "marker.txt")); err != nil || string(b) != "KEEP-ME" {
		t.Errorf("mismatch must not replace live tree; marker = %q err=%v", b, err)
	}
	st, _ := readSyncState(dir)
	if st.Digest != "sha256:old" {
		t.Errorf("mismatch must not rewrite .sync.json; got %+v", st)
	}
}

// --- sync: a good 200 atomically replaces the previous tree ------------

func TestIgSync_ReplacesExistingTreeCleanly(t *testing.T) {
	igHomeDir := t.TempDir()
	t.Setenv("IG_HOME", igHomeDir)
	resetIgFlags()
	defer resetIgFlags()

	dir := filepath.Join(igHomeDir, "projects", "payments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "OLD.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeSyncState(dir, syncState{Digest: "sha256:old"}); err != nil {
		t.Fatal(err)
	}

	tarball := makeTarGz(t, map[string]string{"metadata.json": `{"catalog":"payments"}`, "NEW.txt": "new"})
	etag := sha256Etag(tarball)
	orig := igcatalog.DownloadBundle
	igcatalog.DownloadBundle = func(_, _, _, inm string) ([]byte, string, bool, error) {
		if inm != "sha256:old" {
			t.Errorf("If-None-Match = %q; want sha256:old", inm)
		}
		return tarball, etag, false, nil
	}
	defer func() { igcatalog.DownloadBundle = orig }()

	if _, err := syncOne(testActive(), "payments"); err != nil {
		t.Fatalf("syncOne: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "OLD.txt")); !os.IsNotExist(err) {
		t.Error("old file should be gone after atomic replace")
	}
	if b, err := os.ReadFile(filepath.Join(dir, "NEW.txt")); err != nil || string(b) != "new" {
		t.Errorf("new file missing after replace: %q err=%v", b, err)
	}
	// No leftover temp or backup dirs.
	entries, _ := os.ReadDir(igHomeDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".sync-") {
			t.Errorf("leftover temp dir under IG_HOME: %s", e.Name())
		}
	}
	projEntries, _ := os.ReadDir(filepath.Join(igHomeDir, "projects"))
	for _, e := range projEntries {
		if strings.Contains(e.Name(), ".bak") {
			t.Errorf("leftover backup dir: %s", e.Name())
		}
	}
}

// --- sync: enforces the archive contract (metadata.json at the root) ---

// A well-formed bundle carries the catalog directory's *contents*:
// metadata.json at the archive root plus member/<m>/graphify-out/graph.json.
// It must swap in cleanly and leave metadata.json readable at the tree root.
func TestIgSync_GoodBundleWithMetadataAtRootSwapsIn(t *testing.T) {
	igHomeDir := t.TempDir()
	t.Setenv("IG_HOME", igHomeDir)
	resetIgFlags()
	defer resetIgFlags()

	tarball := makeTarGz(t, map[string]string{
		"metadata.json":                      `{"catalog":"capillary-cloud"}`,
		"member/api/graphify-out/graph.json": `{"m":"api"}`,
		"catalog/graphify-out/graph.json":    `{"root":true}`,
	})
	etag := sha256Etag(tarball)
	orig := igcatalog.DownloadBundle
	igcatalog.DownloadBundle = func(_, _, _, _ string) ([]byte, string, bool, error) {
		return tarball, etag, false, nil
	}
	defer func() { igcatalog.DownloadBundle = orig }()

	if _, err := syncOne(testActive(), "capillary-cloud"); err != nil {
		t.Fatalf("good bundle should sync: %v", err)
	}
	dir := filepath.Join(igHomeDir, "projects", "capillary-cloud")
	if b, err := os.ReadFile(filepath.Join(dir, "metadata.json")); err != nil || string(b) != `{"catalog":"capillary-cloud"}` {
		t.Errorf("metadata.json must land at the catalog root: %q err=%v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "member", "api", "graphify-out", "graph.json")); err != nil || string(b) != `{"m":"api"}` {
		t.Errorf("member graph missing after sync: %q err=%v", b, err)
	}
	// The double-nest must NOT be present.
	if _, err := os.Stat(filepath.Join(dir, "capillary-cloud")); err == nil {
		t.Error("catalog should not be double-nested under itself")
	}
}

// A malformed bundle must be rejected AFTER extraction but BEFORE the swap,
// so the pre-existing live tree is left untouched and readable. The two
// shapes we guard against:
//   - a catalog-prefixed archive (<catalog>/metadata.json, ...) — the exact
//     double-nesting regression we hit in production;
//   - an archive with no metadata.json anywhere.
func TestIgSync_RejectsMalformedBundleAndKeepsLiveTree(t *testing.T) {
	cases := []struct {
		name        string
		files       map[string]string
		wantInError string
	}{
		{
			name: "catalog-prefixed archive",
			files: map[string]string{
				"capillary-cloud/metadata.json":                      `{"catalog":"capillary-cloud"}`,
				"capillary-cloud/member/api/graphify-out/graph.json": `{"m":"api"}`,
			},
			wantInError: "catalog-prefixed",
		},
		{
			name: "no metadata.json anywhere",
			files: map[string]string{
				"member/api/graphify-out/graph.json": `{"m":"api"}`,
			},
			wantInError: "no metadata.json",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			igHomeDir := t.TempDir()
			t.Setenv("IG_HOME", igHomeDir)
			resetIgFlags()
			defer resetIgFlags()

			// Seed a healthy, readable live tree that MUST survive.
			dir := filepath.Join(igHomeDir, "projects", "capillary-cloud")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "metadata.json"), []byte(`LIVE-KEEP-ME`), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := writeSyncState(dir, syncState{Digest: "sha256:old"}); err != nil {
				t.Fatal(err)
			}

			tarball := makeTarGz(t, tc.files)
			etag := sha256Etag(tarball)
			orig := igcatalog.DownloadBundle
			igcatalog.DownloadBundle = func(_, _, _, _ string) ([]byte, string, bool, error) {
				return tarball, etag, false, nil
			}
			defer func() { igcatalog.DownloadBundle = orig }()

			_, err := syncOne(testActive(), "capillary-cloud")
			if err == nil {
				t.Fatal("malformed bundle was silently accepted; want a non-nil contract error")
			}
			if !errors.Is(err, errBundleContract) {
				t.Errorf("error = %v; want errBundleContract", err)
			}
			if !strings.Contains(err.Error(), tc.wantInError) {
				t.Errorf("error %q must mention %q", err.Error(), tc.wantInError)
			}

			// The live tree is untouched and still readable.
			if b, err := os.ReadFile(filepath.Join(dir, "metadata.json")); err != nil || string(b) != "LIVE-KEEP-ME" {
				t.Errorf("live tree must survive a rejected sync; metadata.json = %q err=%v", b, err)
			}
			if st, _ := readSyncState(dir); st.Digest != "sha256:old" {
				t.Errorf("rejected sync must not rewrite .sync.json; got %+v", st)
			}
		})
	}
}

// --- status: compares without downloading the bundle -------------------

func TestIgStatus_ComparesWithoutFetchingBundle(t *testing.T) {
	igHomeDir := t.TempDir()
	t.Setenv("IG_HOME", igHomeDir)
	resetIgFlags()
	defer resetIgFlags()

	dir := filepath.Join(igHomeDir, "projects", "payments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeSyncState(dir, syncState{Digest: "sha256:local"}); err != nil {
		t.Fatal(err)
	}

	origGet := igcatalog.GetCatalog
	igcatalog.GetCatalog = func(_, _, name string) (*igcatalog.Catalog, error) {
		return &igcatalog.Catalog{Name: name, Version: "sha256:server-newer"}, nil
	}
	defer func() { igcatalog.GetCatalog = origGet }()

	origDL := igcatalog.DownloadBundle
	igcatalog.DownloadBundle = func(_, _, _, _ string) ([]byte, string, bool, error) {
		t.Fatal("status must NOT download the bundle")
		return nil, "", false, nil
	}
	defer func() { igcatalog.DownloadBundle = origDL }()

	state, serverVer, localDigest, err := statusOne(testActive(), "payments")
	if err != nil {
		t.Fatalf("statusOne: %v", err)
	}
	if state != "stale" {
		t.Errorf("state = %q; want stale", state)
	}
	if serverVer != "sha256:server-newer" || localDigest != "sha256:local" {
		t.Errorf("serverVer=%q localDigest=%q", serverVer, localDigest)
	}

	igcatalog.GetCatalog = func(_, _, name string) (*igcatalog.Catalog, error) {
		return &igcatalog.Catalog{Name: name, Version: "sha256:local"}, nil
	}
	state, _, _, err = statusOne(testActive(), "payments")
	if err != nil {
		t.Fatal(err)
	}
	if state != "up to date" {
		t.Errorf("state = %q; want up to date", state)
	}
}

// --- publish: gzips graph, reads git/sha from member-meta.json ---------

func TestIgPublish_GzipsGraphAndReadsMeta(t *testing.T) {
	seedIgProfile(t, "default")
	resetIgFlags()
	defer resetIgFlags()

	dir := t.TempDir()
	graphDir := filepath.Join(dir, "member", "api", "graphify-out")
	if err := os.MkdirAll(graphDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(graphDir, "graph.json"), []byte(`{"g":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "member", "api", "member-meta.json"),
		[]byte(`{"git":"https://github.com/acme/api.git","sha":"abc123"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	igPublishCatalog = "payments"
	igPublishMember = "api"

	var gotGz []byte
	var gotGit, gotSha string
	orig := igcatalog.PublishMember
	igcatalog.PublishMember = func(_, _, cat, mem string, gz []byte, git, sha string) error {
		if cat != "payments" || mem != "api" {
			t.Errorf("cat=%q mem=%q", cat, mem)
		}
		gotGz, gotGit, gotSha = gz, git, sha
		return nil
	}
	defer func() { igcatalog.PublishMember = orig }()

	var buf bytes.Buffer
	igPublishCmd.SetOut(&buf)
	igPublishCmd.SetErr(&buf)
	if err := igPublishCmd.RunE(igPublishCmd, []string{dir}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(gotGz))
	if err != nil {
		t.Fatalf("uploaded body not gzip: %v", err)
	}
	raw, _ := io.ReadAll(zr)
	if string(raw) != `{"g":1}` {
		t.Errorf("uploaded graph = %q; want the gzipped graph.json", raw)
	}
	if gotGit != "https://github.com/acme/api.git" || gotSha != "abc123" {
		t.Errorf("git=%q sha=%q; want values from member-meta.json", gotGit, gotSha)
	}
}

// --- claims: one catalog name per line ---------------------------------

func TestIgClaims_OnePerLine(t *testing.T) {
	seedIgProfile(t, "default")
	resetIgFlags()
	defer resetIgFlags()
	igClaimsGit = "https://github.com/acme/api.git"

	orig := igcatalog.Claims
	igcatalog.Claims = func(_, _, git string) ([]string, error) {
		if git != "https://github.com/acme/api.git" {
			t.Errorf("git = %q", git)
		}
		return []string{"payments", "identity"}, nil
	}
	defer func() { igcatalog.Claims = orig }()

	var buf bytes.Buffer
	igClaimsCmd.SetOut(&buf)
	if err := igClaimsCmd.RunE(igClaimsCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if buf.String() != "payments\nidentity\n" {
		t.Errorf("claims output = %q; want one catalog per line", buf.String())
	}
}

// --- manifest push: stamps git_sha + pushed_by -------------------------

func TestIgManifestPush_StampsGitSHA(t *testing.T) {
	seedIgProfile(t, "default")
	resetIgFlags()
	defer resetIgFlags()

	file := filepath.Join(t.TempDir(), "manifest.yaml")
	if err := os.WriteFile(file, []byte("catalog: payments\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	igManifestCatalog = "payments"

	origGit := gitHeadSHA
	gitHeadSHA = func(dir string) (string, error) { return "cafe42", nil }
	defer func() { gitHeadSHA = origGit }()

	var got igcatalog.Manifest
	orig := igcatalog.ManifestPush
	igcatalog.ManifestPush = func(_, _, cat string, m igcatalog.Manifest) error {
		if cat != "payments" {
			t.Errorf("cat = %q", cat)
		}
		got = m
		return nil
	}
	defer func() { igcatalog.ManifestPush = orig }()

	var buf bytes.Buffer
	igManifestPushCmd.SetOut(&buf)
	if err := igManifestPushCmd.RunE(igManifestPushCmd, []string{file}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if got.Content != "catalog: payments\n" {
		t.Errorf("content = %q", got.Content)
	}
	if got.GitSHA != "cafe42" {
		t.Errorf("git_sha = %q; want cafe42 (stamped from the working tree)", got.GitSHA)
	}
	if got.PushedBy != "u@x.com" {
		t.Errorf("pushed_by = %q; want the profile username", got.PushedBy)
	}
	if got.PushedAt == "" {
		t.Error("pushed_at empty")
	}
}

// --- manifest pull: writes served bytes verbatim -----------------------

func TestIgManifestPull_WritesServedBytesVerbatim(t *testing.T) {
	seedIgProfile(t, "default")
	resetIgFlags()
	defer resetIgFlags()

	outFile := filepath.Join(t.TempDir(), "pulled.yaml")
	igManifestOut = outFile

	orig := igcatalog.ManifestPull
	igcatalog.ManifestPull = func(_, _, cat string) (*igcatalog.Manifest, error) {
		if cat != "payments" {
			t.Errorf("cat = %q", cat)
		}
		return &igcatalog.Manifest{Content: "served: true\n"}, nil
	}
	defer func() { igcatalog.ManifestPull = orig }()

	var buf bytes.Buffer
	igManifestPullCmd.SetOut(&buf)
	if err := igManifestPullCmd.RunE(igManifestPullCmd, []string{"payments"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	b, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "served: true\n" {
		t.Errorf("pulled file = %q; want the served bytes verbatim", b)
	}
}

// --- the seven verbs are wired under `ig` ------------------------------

func TestIgCommandTree_HasSevenVerbs(t *testing.T) {
	have := map[string]bool{}
	for _, c := range igCmd.Commands() {
		have[c.Name()] = true
	}
	for _, want := range []string{"list", "sync", "status", "publish", "claims", "manifest"} {
		if !have[want] {
			t.Errorf("praxis ig is missing subcommand %q", want)
		}
	}
	man := map[string]bool{}
	for _, c := range igManifestCmd.Commands() {
		man[c.Name()] = true
	}
	if !man["push"] || !man["pull"] {
		t.Errorf("praxis ig manifest is missing push/pull; have %v", man)
	}
}
