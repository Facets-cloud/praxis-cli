package memory

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubServer spins up an httptest.Server with a request-validating
// handler. The handler asserts auth headers and content-type, and
// returns whatever body the table-row provides.
func stubServer(t *testing.T, wantMethod, wantPath string, status int, body string, wantBearer string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != wantMethod {
			t.Errorf("method = %s; want %s", r.Method, wantMethod)
		}
		if !strings.HasPrefix(r.URL.Path+optionalQuery(r), wantPath) && r.URL.Path != wantPath {
			t.Errorf("path = %s; want prefix %s", r.URL.Path, wantPath)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+wantBearer {
			t.Errorf("auth header = %q; want %q", got, "Bearer "+wantBearer)
		}
		if r.Body != nil && r.Method == http.MethodPost {
			if ct := r.Header.Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q; want application/json", ct)
			}
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func optionalQuery(r *http.Request) string {
	if r.URL.RawQuery != "" {
		return "?" + r.URL.RawQuery
	}
	return ""
}

// --- Recall -----------------------------------------------------------

func TestRecall_HappyPath_ReturnsScoredMatches(t *testing.T) {
	const body = `[
		{"id":"m1","slug":"retry-budgets","title":"Retry budgets","content":"backoff...","relevance_score":1.42,"organization_id":"o","kind":"feedback","audience":"user","category":"fact","importance":"medium","tags":[]},
		{"id":"m2","slug":"backoff","title":"Backoff","content":"...","relevance_score":0.87,"organization_id":"o","kind":"feedback","audience":"user","category":"fact","importance":"medium","tags":[]}
	]`
	srv := stubServer(t, http.MethodPost, "/ai-api/memories/recall", 200, body, "tok")
	got, err := Recall(srv.URL, "tok", RecallRequest{Query: "retry handling", Limit: 5})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d; want 2", len(got))
	}
	if got[0].Slug != "retry-budgets" || got[0].RelevanceScore == nil || *got[0].RelevanceScore != 1.42 {
		t.Errorf("first = %+v", got[0])
	}
}

func TestRecall_EmptyQuery_RejectedClientSide(t *testing.T) {
	// No HTTP call should happen — assert by giving an obviously-broken
	// baseURL so a network call would error differently than "query is
	// required".
	_, err := Recall("http://no-such-host.invalid", "tok", RecallRequest{Query: ""})
	if err == nil || !strings.Contains(err.Error(), "query is required") {
		t.Fatalf("err = %v; want 'query is required'", err)
	}
}

func TestRecall_ServerError_PropagatesStatus(t *testing.T) {
	srv := stubServer(t, http.MethodPost, "/ai-api/memories/recall", 500, `{"detail":"boom"}`, "tok")
	_, err := Recall(srv.URL, "tok", RecallRequest{Query: "x"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("err = %v; want HTTP 500", err)
	}
}

// --- List -------------------------------------------------------------

func TestList_BuildsQueryStringFromParams(t *testing.T) {
	const body = `[{"id":"m1","slug":"x","title":"X","content":"...","organization_id":"o","kind":"user","audience":"user","category":"fact","importance":"medium","tags":[]}]`
	var capturedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		w.WriteHeader(200)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	_, err := List(srv.URL, "tok", ListParams{
		Category:   "fact",
		Importance: "high",
		Tags:       []string{"infra", "ops"},
		Limit:      10,
		Offset:     20,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	// Order is not guaranteed by url.Values.Encode (it sorts keys), so
	// assert all expected pieces are present rather than full equality.
	expected := []string{
		"category=fact",
		"importance=high",
		"tags=infra%2Cops",
		"limit=10",
		"offset=20",
	}
	for _, want := range expected {
		if !strings.Contains(capturedQuery, want) {
			t.Errorf("query %q missing %q", capturedQuery, want)
		}
	}
}

func TestList_OmitsEmptyParams(t *testing.T) {
	const body = `[]`
	var capturedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		w.WriteHeader(200)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	if _, err := List(srv.URL, "tok", ListParams{}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if capturedQuery != "" {
		t.Errorf("expected empty query; got %q", capturedQuery)
	}
}

// --- Create -----------------------------------------------------------

func TestCreate_PostsBodyWithoutAgentID(t *testing.T) {
	const body = `{"id":"m1","slug":"new-fact","title":"New fact","content":"facts","organization_id":"o","kind":"user","audience":"user","category":"fact","importance":"medium","tags":[]}`
	var capturedQuery string
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	got, err := Create(srv.URL, "tok", CreateRequest{
		Title:    "New fact",
		Content:  "facts",
		Audience: AudienceUser,
		Tags:     []string{"infra"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got == nil || got.Slug != "new-fact" {
		t.Errorf("got = %+v", got)
	}
	if strings.Contains(capturedQuery, "agent_id") {
		t.Errorf("query carried agent_id but should not: %s", capturedQuery)
	}

	var parsed CreateRequest
	if err := json.Unmarshal(capturedBody, &parsed); err != nil {
		t.Fatalf("could not parse body: %v", err)
	}
	if parsed.Title != "New fact" || parsed.Content != "facts" || parsed.Audience != AudienceUser {
		t.Errorf("body = %+v", parsed)
	}
}

func TestCreate_MissingFields_RejectedClientSide(t *testing.T) {
	tests := []struct {
		name string
		req  CreateRequest
		want string
	}{
		{"missing title", CreateRequest{Content: "x"}, "title is required"},
		{"missing content", CreateRequest{Title: "x"}, "content is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Create("http://no-such-host.invalid", "tok", tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v; want %q", err, tt.want)
			}
		})
	}
}

// --- Transport guards -------------------------------------------------

func TestDoJSON_RejectsEmptyBaseURLOrToken(t *testing.T) {
	tests := []struct {
		name, baseURL, token, want string
	}{
		{"no baseURL", "", "tok", "baseURL is required"},
		{"no token", "http://x", "", "token is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Recall(tt.baseURL, tt.token, RecallRequest{Query: "x"})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v; want %q", err, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"under cap", "abc", 10, "abc"},
		{"at cap", "abcde", 5, "abcde"},
		{"over cap", "abcdef", 3, "abc…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := truncate(tt.in, tt.max); got != tt.want {
				t.Errorf("got %q; want %q", got, tt.want)
			}
		})
	}
}
