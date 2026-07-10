// Package igcatalog is the REST client for the ig catalog server on a
// Praxis deployment (mounted at /ai-api/ig, org-scoped). It is the ONLY
// network client of that server: `praxis ig <verb>` calls here; the `ig`
// binary itself never learns servers exist — it reads the filesystem that
// `praxis ig sync` materializes.
//
// The package mirrors the layout of internal/duties and internal/memory:
// typed structs track the server's response models, exported function vars
// give tests a seam to swap, and every transport call sends
// Authorization: Bearer <token> (the same bearer these clients already
// send — the server resolves it via auth_service.validate_user()).
//
// Backend routes (all under /ai-api/ig, org-scoped):
//
//	GET  /catalogs                       list the org's catalogs
//	GET  /catalogs/claims?git=<url>      names of catalogs claiming a repo
//	GET  /catalogs/{c}                   one catalog's summary (404 if absent)
//	POST /catalogs/{c}/members/{m}       publish one member (gzipped graph.json)
//	GET  /catalogs/{c}/bundle            assembled catalog as a gzipped tarball
//	POST /catalogs/{c}/manifest          push the manifest text + stamps
//	GET  /catalogs/{c}/manifest          pull the manifest text + stamps
package igcatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// apiPrefix is the deployment's ig-API mount (mirrors
	// internal/memory's /ai-api/memories and duties' /ai-api).
	apiPrefix = "/ai-api/ig"

	defaultTimeout = 30 * time.Second

	// bundleTimeout is longer than defaultTimeout: a catalog bundle is a
	// gzipped tarball of the whole graph-of-graphs and can be large.
	bundleTimeout = 120 * time.Second
)

// Catalog is the wire shape of a catalog summary: {name, version,
// built_at, members[]}. Only the fields the CLI surfaces are retained —
// encoding/json ignores the rest, so older binaries keep working as the
// server adds fields.
//
// Version doubles as the bundle's ETag / the stored .sync.json digest: it
// is the single opaque token the server hands out to identify a built
// catalog. `praxis ig status` compares the local digest against this
// Version without downloading the bundle; `praxis ig sync` sends it as
// If-None-Match to get a cheap 304 when nothing changed.
type Catalog struct {
	Name    string   `json:"name"`
	Version string   `json:"version"`
	BuiltAt string   `json:"built_at"`
	Members []Member `json:"members"`
}

// Member is the wire shape of one catalog member, mirroring the entries in
// ig's own metadata.json: a name, a kind ("code" or "infra"), and — for
// code members — the canonical git URL and the built commit sha. The infra
// member carries no repo, so Git/SHA are absent or JSON null on the wire;
// both decode to the empty string. omitempty keeps `praxis ig list --json`
// output tidy for members without a repo.
type Member struct {
	Name string `json:"name"`
	Kind string `json:"kind,omitempty"`
	Git  string `json:"git,omitempty"`
	SHA  string `json:"sha,omitempty"`
}

// Manifest is the wire shape of a served manifest (the server's IgManifest):
// the catalog it belongs to, the text, plus the stamps the server records on
// push (who pushed it, when, and the git sha of the working tree the file came
// from). All four of Catalog/Content/PushedBy/PushedAt are required on the
// wire; GitSHA is nullable. Catalog is retained so the response type captures
// every field the server declares required, even though the CLI currently only
// surfaces Content.
type Manifest struct {
	Catalog  string `json:"catalog,omitempty"`
	Content  string `json:"content"`
	PushedBy string `json:"pushed_by,omitempty"`
	PushedAt string `json:"pushed_at,omitempty"`
	GitSHA   string `json:"git_sha,omitempty"`
}

// manifestPushRequest is the wire shape the server accepts on manifest push
// (its IgManifestPushRequest): ONLY the text and an optional git sha. The
// server stamps pushed_by (from the bearer's identity) and pushed_at (server
// time) itself, so the client must NOT send them — reusing the Manifest
// response type here would put fields on the wire the server never declared.
type manifestPushRequest struct {
	Content string `json:"content"`
	GitSHA  string `json:"git_sha,omitempty"`
}

// claimsResponse is the envelope the server returns from GET /catalogs/claims
// (its IgClaimsResponse): the echoed git URL plus the names of the catalogs
// claiming it. The server does NOT return a bare []string — decoding it as one
// is what made `praxis ig claims` die with "cannot unmarshal object into Go
// value of type []string". Only Git is required; Catalogs is absent (nil) when
// no catalog claims the repo.
type claimsResponse struct {
	Git      string   `json:"git"`
	Catalogs []string `json:"catalogs"`
}

// --- HTTP seams — tests swap these to avoid the network. ---------------

// ListCatalogs returns every catalog in the org.
var ListCatalogs = func(baseURL, token string) ([]Catalog, error) {
	return doJSON[[]Catalog](baseURL, token, http.MethodGet, apiPrefix+"/catalogs", nil)
}

// GetCatalog returns one catalog's summary. The server 404s when the
// catalog is absent; that surfaces as an `HTTP 404 …` error.
var GetCatalog = func(baseURL, token, name string) (*Catalog, error) {
	if name == "" {
		return nil, fmt.Errorf("catalog name is required")
	}
	c, err := doJSON[Catalog](baseURL, token, http.MethodGet,
		apiPrefix+"/catalogs/"+url.PathEscape(name), nil)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// Claims returns the names of catalogs that have a member whose canonical
// git URL matches git. Repo CI loops over these to know which catalogs to
// refresh after a push.
var Claims = func(baseURL, token, git string) ([]string, error) {
	if git == "" {
		return nil, fmt.Errorf("git url is required")
	}
	q := url.Values{}
	q.Set("git", git)
	env, err := doJSON[claimsResponse](baseURL, token, http.MethodGet,
		apiPrefix+"/catalogs/claims?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	return env.Catalogs, nil
}

// PublishMember uploads one member's gzipped graph.json to a catalog,
// stamping the member's canonical git URL and commit sha. Server-side it
// is idempotent — republishing the same (git, sha) is accepted.
//
// The server handler (publish_member) expects multipart/form-data: a file
// part named "graph" carrying the gzipped graph.json bytes, plus optional
// "git"/"sha" form fields. On the server those are Optional[...] = Form(None),
// so they are written only when non-empty; git/sha are NOT query parameters.
var PublishMember = func(baseURL, token, catalog, member string, gzGraph []byte, git, sha string) error {
	if catalog == "" || member == "" {
		return fmt.Errorf("catalog and member are required")
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("graph", "graph.json.gz")
	if err != nil {
		return err
	}
	if _, err := part.Write(gzGraph); err != nil {
		return err
	}
	if git != "" {
		if err := writer.WriteField("git", git); err != nil {
			return err
		}
	}
	if sha != "" {
		if err := writer.WriteField("sha", sha); err != nil {
			return err
		}
	}
	if err := writer.Close(); err != nil {
		return err
	}

	// FormDataContentType() carries the boundary — never hand-roll it.
	path := apiPrefix + "/catalogs/" + url.PathEscape(catalog) + "/members/" + url.PathEscape(member)
	return sendBytes(baseURL, token, http.MethodPost, path, writer.FormDataContentType(), body.Bytes())
}

// DownloadBundle fetches the assembled catalog as a gzipped tarball.
// ifNoneMatch is the caller's last-synced digest; when it matches the
// server's current ETag the server returns 304 and this reports
// notModified=true with an empty body (a cheap no-op re-sync). On 200 it
// returns the tarball bytes and the ETag (the new digest).
var DownloadBundle = func(baseURL, token, catalog, ifNoneMatch string) (body []byte, etag string, notModified bool, err error) {
	if baseURL == "" {
		return nil, "", false, fmt.Errorf("baseURL is required")
	}
	if token == "" {
		return nil, "", false, fmt.Errorf("token is required")
	}
	if catalog == "" {
		return nil, "", false, fmt.Errorf("catalog is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), bundleTimeout)
	defer cancel()

	full := strings.TrimRight(baseURL, "/") + apiPrefix + "/catalogs/" + url.PathEscape(catalog) + "/bundle"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return nil, "", false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}

	client := &http.Client{Timeout: bundleTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", false, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotModified {
		return nil, resp.Header.Get("ETag"), true, nil
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", false, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", false, fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, full, truncate(string(raw), 200))
	}
	return raw, resp.Header.Get("ETag"), false, nil
}

// ManifestPush uploads the manifest text and its git sha. The server stamps
// pushed_by/pushed_at itself, so only content and git_sha go on the wire (the
// server's IgManifestPushRequest) — m.PushedBy/m.PushedAt are ignored here and
// exist only for the cmd layer's local echo.
var ManifestPush = func(baseURL, token, catalog string, m Manifest) error {
	if catalog == "" {
		return fmt.Errorf("catalog is required")
	}
	body, err := json.Marshal(manifestPushRequest{Content: m.Content, GitSHA: m.GitSHA})
	if err != nil {
		return err
	}
	path := apiPrefix + "/catalogs/" + url.PathEscape(catalog) + "/manifest"
	return sendBytes(baseURL, token, http.MethodPost, path, "application/json", body)
}

// ManifestPull fetches the served manifest (text + stamps) so a builder
// can diff it against their local copy.
var ManifestPull = func(baseURL, token, catalog string) (*Manifest, error) {
	if catalog == "" {
		return nil, fmt.Errorf("catalog is required")
	}
	m, err := doJSON[Manifest](baseURL, token, http.MethodGet,
		apiPrefix+"/catalogs/"+url.PathEscape(catalog)+"/manifest", nil)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// --- transport ---------------------------------------------------------

// doJSON is the shared JSON transport. Returns a typed payload or an error
// shaped as `HTTP <status> from <url>: <body-prefix>` so the cmd layer can
// branch on status (401/403 → auth) without re-parsing the URL. Copied
// deliberately from internal/duties to keep the clients' error contracts
// identical.
func doJSON[T any](baseURL, token, method, path string, body io.Reader) (T, error) {
	var zero T
	if baseURL == "" {
		return zero, fmt.Errorf("baseURL is required")
	}
	if token == "" {
		return zero, fmt.Errorf("token is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	full := strings.TrimRight(baseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, full, body)
	if err != nil {
		return zero, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: defaultTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return zero, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return zero, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return zero, fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, full, truncate(string(raw), 200))
	}

	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		return zero, fmt.Errorf("parse response: %w", err)
	}
	return out, nil
}

// sendBytes POSTs (or PUTs) a raw body with the bearer header and a caller-
// chosen Content-Type, tolerating any 2xx with any (or empty) response
// body. Used for the two non-JSON-returning uploads: the gzipped member
// graph and the manifest push. The error contract matches doJSON so the
// cmd layer's reportHTTPErr dispatch works the same.
func sendBytes(baseURL, token, method, path, contentType string, body []byte) error {
	if baseURL == "" {
		return fmt.Errorf("baseURL is required")
	}
	if token == "" {
		return fmt.Errorf("token is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	full := strings.TrimRight(baseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, full, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	client := &http.Client{Timeout: defaultTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, full, truncate(string(raw), 200))
	}
	return nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
