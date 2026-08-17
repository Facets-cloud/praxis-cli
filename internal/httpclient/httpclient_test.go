package httpclient

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// headerEcho records the auth headers of the LAST request it sees.
type headerEcho struct {
	auth, facets string
	method       string
	body         string
}

func (h *headerEcho) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h.auth = r.Header.Get("Authorization")
		h.facets = r.Header.Get("X-Facets-Username")
		h.method = r.Method
		b, _ := io.ReadAll(r.Body)
		h.body = string(b)
		w.WriteHeader(http.StatusOK)
	}
}

func TestNew_DropsAuthHeadersOnForeignRedirect(t *testing.T) {
	var got headerEcho
	target := httptest.NewServer(got.handler())
	defer target.Close()

	// The redirector's host (127.0.0.1) is a different domain than the
	// target's — httptest gives both 127.0.0.1 but different ports, so force
	// a genuinely foreign hostname via the Location header.
	from := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.Replace(target.URL, "127.0.0.1", "localhost", 1), http.StatusTemporaryRedirect)
	}))
	defer from.Close()

	req, err := http.NewRequest("POST", from.URL, strings.NewReader(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer pat123")
	req.Header.Set("X-Facets-Username", "user@corp")

	resp, err := New(5 * time.Second).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if got.auth != "" {
		t.Errorf("Authorization survived a foreign redirect: %q", got.auth)
	}
	if got.facets != "" {
		t.Errorf("X-Facets-Username survived a foreign redirect: %q", got.facets)
	}
	// Method and body must still survive — that's the other half of the policy.
	if got.method != "POST" || got.body != `{"a":1}` {
		t.Errorf("redirect lost method/body: got %s %q", got.method, got.body)
	}
}

func TestNew_KeepsAuthHeadersOnSameHostRedirect(t *testing.T) {
	var got headerEcho
	mux := http.NewServeMux()
	mux.HandleFunc("/final", got.handler())
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/final", http.StatusMovedPermanently)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	req, err := http.NewRequest("POST", srv.URL+"/start", strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer pat123")
	req.Header.Set("X-Facets-Username", "user@corp")

	resp, err := New(5 * time.Second).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if got.auth != "Bearer pat123" || got.facets != "user@corp" {
		t.Errorf("same-host redirect dropped auth: auth=%q facets=%q", got.auth, got.facets)
	}
	if got.method != "POST" || got.body != "payload" {
		t.Errorf("same-host redirect lost method/body: got %s %q", got.method, got.body)
	}
}

func TestNew_StopsAfterTenRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/next", http.StatusFound)
	}))
	defer srv.Close()

	if _, err := New(5 * time.Second).Get(srv.URL); err == nil {
		t.Error("want an error on an endless redirect loop")
	}
}

func TestIsDomainOrSubdomain(t *testing.T) {
	tests := []struct {
		child, parent string
		want          bool
	}{
		{"cp.test", "cp.test", true},
		{"CP.TEST", "cp.test", true},
		{"www.cp.test", "cp.test", true},
		{"cp.test", "www.cp.test", false},
		{"evil.example", "cp.test", false},
		{"notcp.test", "cp.test", false}, // suffix must be label-aligned
		{"localhost", "127.0.0.1", false},
	}
	for _, tt := range tests {
		if got := isDomainOrSubdomain(tt.child, tt.parent); got != tt.want {
			t.Errorf("isDomainOrSubdomain(%q, %q) = %v, want %v", tt.child, tt.parent, got, tt.want)
		}
	}
}

// checkRedirect is unit-tested directly for the scheme/cookie rules: an
// https→http redirect can't be built from httptest without wiring a trusted
// TLS cert into the client under test.
func TestCheckRedirect_StripsSensitiveHeaders(t *testing.T) {
	tests := []struct {
		name     string
		from, to string
		wantKept bool
	}{
		{"same host keeps everything", "https://cp.test/a", "https://cp.test/b", true},
		{"subdomain keeps everything", "https://cp.test/a", "https://www.cp.test/b", true},
		{"foreign host strips", "https://cp.test/a", "https://evil.example/b", false},
		{"https→http on the SAME host strips", "https://cp.test/a", "http://cp.test/b", false},
		{"http→http is not a downgrade", "http://cp.test/a", "http://cp.test/b", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig, err := http.NewRequest("GET", tt.from, nil)
			if err != nil {
				t.Fatal(err)
			}
			// Mirror what net/http hands CheckRedirect: the header set it
			// already prepared for the NEW request.
			req, err := http.NewRequest("GET", tt.to, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, h := range []string{"Authorization", "Cookie", "X-Facets-Username"} {
				req.Header.Set(h, "secret")
			}
			if err := checkRedirect(req, []*http.Request{orig}); err != nil {
				t.Fatal(err)
			}
			for _, h := range []string{"Authorization", "Cookie", "X-Facets-Username"} {
				got := req.Header.Get(h) != ""
				if got != tt.wantKept {
					t.Errorf("%s kept = %v, want %v", h, got, tt.wantKept)
				}
			}
		})
	}
}

// The old policy cloned the original headers over the ones net/http prepared,
// which restored the Cookie Go had just stripped for a foreign host.
func TestNew_DropsCookieOnForeignRedirect(t *testing.T) {
	var gotCookie string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	from := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.Replace(target.URL, "127.0.0.1", "localhost", 1), http.StatusFound)
	}))
	defer from.Close()

	req, err := http.NewRequest("GET", from.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Cookie", "session=abc")

	resp, err := New(5 * time.Second).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if gotCookie != "" {
		t.Errorf("Cookie survived a foreign redirect: %q", gotCookie)
	}
}
