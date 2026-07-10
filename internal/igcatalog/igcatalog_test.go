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
		{"name":"payments","version":"sha256:aaa","built_at":"2026-07-09T10:00:00Z","members":[{"name":"api","kind":"code","git":"github.com/acme/api","sha":"a1"},{"name":"web","kind":"code","git":"github.com/acme/web","sha":"b2"}]},
		{"name":"identity","version":"sha256:bbb","built_at":"2026-07-08T09:00:00Z","members":[{"name":"idp","kind":"code","git":"github.com/acme/idp","sha":"c3"}]}
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

// --- ListCatalogs: member objects (server contract) --------------------
//
// The live server returns member OBJECTS ({name,kind,git,sha}), mirroring
// ig's own metadata.json — not a list of bare member-name strings. This is
// the exact payload `praxis ig list` choked on before Catalog.Members
// became []Member.

func assertMember(t *testing.T, m Member, name, kind, git, sha string) {
	t.Helper()
	if m.Name != name || m.Kind != kind || m.Git != git || m.SHA != sha {
		t.Errorf("member = %+v; want {Name:%s Kind:%s Git:%s SHA:%s}", m, name, kind, git, sha)
	}
}

func TestListCatalogs_DecodesMemberObjects(t *testing.T) {
	const body = `[
  {
    "name": "capillary-cloud",
    "version": "2026.07.10-104039",
    "built_at": "2026-07-10T10:40:39Z",
    "members": [
      {"name": "control-plane",          "kind": "code", "git": "github.com/facets-cloud/control-plane",          "sha": "9a7c76c51a85da8dee3307bac95f85318961e8f9"},
      {"name": "control-plane-ui-react", "kind": "code", "git": "github.com/facets-cloud/control-plane-ui-react", "sha": "6fa97644570cc1b38968a350e8e63c723cc5b33b"},
      {"name": "raptor",                 "kind": "code", "git": "github.com/facets-cloud/raptor",                  "sha": "5032459d14e117d6b45033fce49c862ce588c842"},
      {"name": "agent-factory",          "kind": "code", "git": "github.com/facets-cloud/agent-factory",           "sha": "ae22e27b9d3f4a5c6e7f8091a2b3c4d5e6f70819"}
    ]
  }
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
	if len(got) != 1 {
		t.Fatalf("got %d catalogs; want 1: %+v", len(got), got)
	}
	c := got[0]
	if c.Name != "capillary-cloud" || c.Version != "2026.07.10-104039" {
		t.Errorf("catalog head = %+v", c)
	}
	if len(c.Members) != 4 {
		t.Fatalf("got %d members; want 4: %+v", len(c.Members), c.Members)
	}
	assertMember(t, c.Members[0], "control-plane", "code",
		"github.com/facets-cloud/control-plane", "9a7c76c51a85da8dee3307bac95f85318961e8f9")
	assertMember(t, c.Members[2], "raptor", "code",
		"github.com/facets-cloud/raptor", "5032459d14e117d6b45033fce49c862ce588c842")
	assertMember(t, c.Members[3], "agent-factory", "code",
		"github.com/facets-cloud/agent-factory", "ae22e27b9d3f4a5c6e7f8091a2b3c4d5e6f70819")
}

// An infra member carries no repo: git/sha are absent (or explicit null).
// It must decode without error, leaving Git/SHA empty.
func TestListCatalogs_InfraMemberHasNoRepo(t *testing.T) {
	const body = `[
  {"name":"cap","version":"v1","built_at":"2026-07-10T10:40:39Z","members":[
    {"name":"platform","kind":"code","git":"github.com/facets-cloud/platform","sha":"deadbeef"},
    {"name":"infra","kind":"infra"},
    {"name":"infra-null","kind":"infra","git":null,"sha":null}
  ]}
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
	if len(got) != 1 || len(got[0].Members) != 3 {
		t.Fatalf("got = %+v", got)
	}
	assertMember(t, got[0].Members[1], "infra", "infra", "", "")
	// A member whose git/sha are explicit JSON null must also be tolerated.
	assertMember(t, got[0].Members[2], "infra-null", "infra", "", "")
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

// --- PublishMember (multipart/form-data upload) ------------------------
//
// Contract (server handler publish_member): multipart/form-data with a file
// part named "graph" carrying the gzipped graph.json bytes, plus optional
// "git"/"sha" form fields (Optional[...] = Form(None)). git/sha are NOT
// query parameters, and are omitted entirely when empty.

func gzipOf(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(s)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func TestPublishMember_UploadsMultipartWithGitAndSha(t *testing.T) {
	const graph = `{"nodes":[{"id":"n1"}]}`
	gz := gzipOf(t, graph)

	hit := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit++
		assertReq(t, r, http.MethodPost, "/ai-api/ig/catalogs/payments/members/api", "tok")

		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "multipart/form-data") {
			t.Errorf("content-type = %q; want multipart/form-data", ct)
		}
		// git/sha must travel as form fields, never as query params.
		if q := r.URL.Query().Get("git"); q != "" {
			t.Errorf("git leaked into query string: %q", q)
		}
		if q := r.URL.Query().Get("sha"); q != "" {
			t.Errorf("sha leaked into query string: %q", q)
		}

		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("body is not multipart/form-data: %v", err)
		}
		if got := r.FormValue("git"); got != "https://github.com/acme/api.git" {
			t.Errorf("git form value = %q", got)
		}
		if got := r.FormValue("sha"); got != "abc123" {
			t.Errorf("sha form value = %q", got)
		}

		f, hdr, err := r.FormFile("graph")
		if err != nil {
			t.Fatalf("graph file part missing: %v", err)
		}
		defer func() { _ = f.Close() }()
		if hdr.Filename == "" {
			t.Error("graph part has no filename")
		}
		got, _ := io.ReadAll(f)
		if !bytes.Equal(got, gz) {
			t.Errorf("graph part bytes = %q; want the gzipped graph", got)
		}
		// The uploaded bytes are the gzip we handed in, decompressible.
		zr, err := gzip.NewReader(bytes.NewReader(got))
		if err != nil {
			t.Fatalf("graph part is not gzip: %v", err)
		}
		raw, _ := io.ReadAll(zr)
		if string(raw) != graph {
			t.Errorf("decompressed graph = %q; want %q", raw, graph)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// First publish, then a repeat — the server accepts both (idempotent).
	for i := 0; i < 2; i++ {
		if err := PublishMember(srv.URL, "tok", "payments", "api", gz, "https://github.com/acme/api.git", "abc123"); err != nil {
			t.Fatalf("PublishMember #%d: %v", i, err)
		}
	}
	if hit != 2 {
		t.Errorf("handler hit %d times; want 2", hit)
	}
}

func TestPublishMember_OmitsEmptyGitAndSha(t *testing.T) {
	const graph = `{"nodes":[]}`
	gz := gzipOf(t, graph)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertReq(t, r, http.MethodPost, "/ai-api/ig/catalogs/payments/members/api", "tok")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("body is not multipart/form-data: %v", err)
		}
		// Empty git/sha are absent — not sent as empty fields.
		if vals, ok := r.MultipartForm.Value["git"]; ok {
			t.Errorf("git should be absent when empty, got %v", vals)
		}
		if vals, ok := r.MultipartForm.Value["sha"]; ok {
			t.Errorf("sha should be absent when empty, got %v", vals)
		}
		// The graph part is still present and intact.
		f, _, err := r.FormFile("graph")
		if err != nil {
			t.Fatalf("graph file part missing: %v", err)
		}
		defer func() { _ = f.Close() }()
		got, _ := io.ReadAll(f)
		if !bytes.Equal(got, gz) {
			t.Errorf("graph part bytes mismatch")
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	if err := PublishMember(srv.URL, "tok", "payments", "api", gz, "", ""); err != nil {
		t.Fatalf("PublishMember: %v", err)
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
