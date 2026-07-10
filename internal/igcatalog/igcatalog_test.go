package igcatalog

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func assertReq(t *testing.T, r *http.Request, wantMethod, wantPath, wantBearer string) {
	t.Helper()
	if r.Method != wantMethod {
		t.Errorf("method = %s; want %s", r.Method, wantMethod)
	}
	if r.URL.Path != wantPath {
		t.Errorf("path = %s; want %s", r.URL.Path, wantPath)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer "+wantBearer {
		t.Errorf("auth header = %q; want %q", got, "Bearer "+wantBearer)
	}
}

// --- ListCatalogs ------------------------------------------------------

func TestListCatalogs_HappyPath(t *testing.T) {
	const body = `[
		{"name":"payments","version":"sha256:aaa","built_at":"2026-07-09T10:00:00Z","members":["api","web"]},
		{"name":"identity","version":"sha256:bbb","built_at":"2026-07-08T09:00:00Z","members":["idp"]}
	]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertReq(t, r, http.MethodGet, "/ai-api/ig/catalogs", "tok")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	got, err := ListCatalogs(srv.URL, "tok")
	if err != nil {
		t.Fatalf("ListCatalogs: %v", err)
	}
	if len(got) != 2 || got[0].Name != "payments" || len(got[0].Members) != 2 {
		t.Fatalf("got = %+v", got)
	}
	if got[0].Version != "sha256:aaa" {
		t.Errorf("version = %q", got[0].Version)
	}
}

// --- GetCatalog --------------------------------------------------------

func TestGetCatalog_404SurfacesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertReq(t, r, http.MethodGet, "/ai-api/ig/catalogs/ghost", "tok")
		w.WriteHeader(404)
		_, _ = w.Write([]byte("no such catalog"))
	}))
	defer srv.Close()

	_, err := GetCatalog(srv.URL, "tok", "ghost")
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("err = %v; want HTTP 404", err)
	}
}

// --- Claims ------------------------------------------------------------

func TestClaims_EncodesGitAndReturnsNames(t *testing.T) {
	const git = "https://github.com/acme/api.git"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertReq(t, r, http.MethodGet, "/ai-api/ig/catalogs/claims", "tok")
		if r.URL.Query().Get("git") != git {
			t.Errorf("git = %q; want %q", r.URL.Query().Get("git"), git)
		}
		_, _ = w.Write([]byte(`["payments","identity"]`))
	}))
	defer srv.Close()

	got, err := Claims(srv.URL, "tok", git)
	if err != nil {
		t.Fatalf("Claims: %v", err)
	}
	if len(got) != 2 || got[0] != "payments" || got[1] != "identity" {
		t.Errorf("got = %+v", got)
	}
}

// --- PublishMember (gzip upload + git/sha query) -----------------------

func TestPublishMember_UploadsGzipWithGitAndSha(t *testing.T) {
	const graph = `{"nodes":[{"id":"n1"}]}`
	hit := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit++
		assertReq(t, r, http.MethodPost, "/ai-api/ig/catalogs/payments/members/api", "tok")
		if r.URL.Query().Get("git") != "https://github.com/acme/api.git" {
			t.Errorf("git = %q", r.URL.Query().Get("git"))
		}
		if r.URL.Query().Get("sha") != "abc123" {
			t.Errorf("sha = %q", r.URL.Query().Get("sha"))
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/gzip" {
			t.Errorf("content-type = %q; want application/gzip", ct)
		}
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Fatalf("body not gzip: %v", err)
		}
		raw, _ := io.ReadAll(zr)
		if string(raw) != graph {
			t.Errorf("decompressed body = %q; want %q", raw, graph)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write([]byte(graph))
	_ = zw.Close()

	// First publish, then a repeat — the server accepts both (idempotent).
	for i := 0; i < 2; i++ {
		if err := PublishMember(srv.URL, "tok", "payments", "api", buf.Bytes(), "https://github.com/acme/api.git", "abc123"); err != nil {
			t.Fatalf("PublishMember #%d: %v", i, err)
		}
	}
	if hit != 2 {
		t.Errorf("handler hit %d times; want 2", hit)
	}
}

// --- DownloadBundle (200 with ETag, and 304 no-op) ---------------------

func TestDownloadBundle_ReturnsBytesAndETag(t *testing.T) {
	const tarball = "\x1f\x8bfake-gzipped-tarball"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertReq(t, r, http.MethodGet, "/ai-api/ig/catalogs/payments/bundle", "tok")
		w.Header().Set("ETag", "sha256:deadbeef")
		_, _ = w.Write([]byte(tarball))
	}))
	defer srv.Close()

	body, etag, notModified, err := DownloadBundle(srv.URL, "tok", "payments", "")
	if err != nil {
		t.Fatalf("DownloadBundle: %v", err)
	}
	if notModified {
		t.Error("notModified should be false on 200")
	}
	if string(body) != tarball {
		t.Errorf("body = %q", body)
	}
	if etag != "sha256:deadbeef" {
		t.Errorf("etag = %q", etag)
	}
}

func TestDownloadBundle_SendsIfNoneMatchAnd304IsNoOp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if inm := r.Header.Get("If-None-Match"); inm != "sha256:current" {
			t.Errorf("If-None-Match = %q; want sha256:current", inm)
		}
		w.Header().Set("ETag", "sha256:current")
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	body, etag, notModified, err := DownloadBundle(srv.URL, "tok", "payments", "sha256:current")
	if err != nil {
		t.Fatalf("DownloadBundle: %v", err)
	}
	if !notModified {
		t.Error("304 must report notModified=true")
	}
	if len(body) != 0 {
		t.Errorf("304 must have empty body, got %q", body)
	}
	if etag != "sha256:current" {
		t.Errorf("etag = %q", etag)
	}
}

// --- Manifest push / pull ----------------------------------------------

func TestManifestPush_SendsContentAndStamps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertReq(t, r, http.MethodPost, "/ai-api/ig/catalogs/payments/manifest", "tok")
		raw, _ := io.ReadAll(r.Body)
		s := string(raw)
		for _, want := range []string{`"content":"manifest-body"`, `"git_sha":"cafe42"`, `"pushed_by":"u@x.com"`} {
			if !strings.Contains(s, want) {
				t.Errorf("push body missing %q\ngot: %s", want, s)
			}
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	err := ManifestPush(srv.URL, "tok", "payments", Manifest{
		Content: "manifest-body", PushedBy: "u@x.com", PushedAt: "2026-07-09T10:00:00Z", GitSHA: "cafe42",
	})
	if err != nil {
		t.Fatalf("ManifestPush: %v", err)
	}
}

func TestManifestPull_ReturnsServedManifest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertReq(t, r, http.MethodGet, "/ai-api/ig/catalogs/payments/manifest", "tok")
		_, _ = w.Write([]byte(`{"content":"served-manifest","pushed_by":"a@b","git_sha":"c0ffee"}`))
	}))
	defer srv.Close()

	m, err := ManifestPull(srv.URL, "tok", "payments")
	if err != nil {
		t.Fatalf("ManifestPull: %v", err)
	}
	if m.Content != "served-manifest" || m.GitSHA != "c0ffee" {
		t.Errorf("manifest = %+v", m)
	}
}

// --- transport edge: token/baseURL required ----------------------------

func TestRequiresBaseURLAndToken(t *testing.T) {
	if _, err := ListCatalogs("", "tok"); err == nil {
		t.Error("want error for empty baseURL")
	}
	if _, err := ListCatalogs("http://x.test", ""); err == nil {
		t.Error("want error for empty token")
	}
	if _, _, _, err := DownloadBundle("", "tok", "c", ""); err == nil {
		t.Error("want error for empty baseURL on DownloadBundle")
	}
}
