package skillcatalog

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetch_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ai-api/v1/skills/bundle" {
			t.Errorf("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk_test_X" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"name":"k8s-operations","display_name":"K8s Ops","description":"Use when …","scope":"global","content":"# Body","triggers":["kubectl"]},
			{"name":"my-skill","scope":"organization","content":"# Org body","display_name":"My Skill","description":"d"}
		]`))
	}))
	defer srv.Close()

	skills, err := Fetch(srv.URL, "sk_test_X")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("got %d skills; want 2", len(skills))
	}
	if skills[0].Name != "k8s-operations" || skills[0].Content != "# Body" {
		t.Errorf("first skill = %+v", skills[0])
	}
	if got := skills[0].PrefixedName(); got != "praxis-k8s-operations" {
		t.Errorf("PrefixedName() = %q", got)
	}
}

func TestFetch_HTTPError_IncludesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"Authentication failed"}`))
	}))
	defer srv.Close()

	_, err := Fetch(srv.URL, "bad_token")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "Authentication failed") {
		t.Errorf("err = %v", err)
	}
}

func TestFetch_RequiresURLAndToken(t *testing.T) {
	if _, err := Fetch("", "t"); err == nil {
		t.Error("expected error for empty URL")
	}
	if _, err := Fetch("https://x", ""); err == nil {
		t.Error("expected error for empty token")
	}
}

func TestFetch_TrailingSlashURL(t *testing.T) {
	got := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	if _, err := Fetch(srv.URL+"/", "t"); err != nil {
		t.Fatal(err)
	}
	// Path should NOT have double slash
	if got != "/ai-api/v1/skills/bundle" {
		t.Errorf("path = %q (expected single slash)", got)
	}
}

func TestFetch_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	_, err := Fetch(srv.URL, "t")
	if err == nil || !strings.Contains(err.Error(), "parse bundle") {
		t.Errorf("err = %v", err)
	}
}

func TestPrefixedName_AlwaysPrefixed(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"k8s-ops", "praxis-k8s-ops"},
		{"already-praxis-prefix-but-still-prefixed-once-more", "praxis-already-praxis-prefix-but-still-prefixed-once-more"},
		// We do NOT collapse double-prefixes — keeps the rule "if it starts
		// with praxis-, the CLI installed it" mechanical.
	}
	for _, tc := range cases {
		s := Skill{Name: tc.in}
		if got := s.PrefixedName(); got != tc.want {
			t.Errorf("PrefixedName(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}
