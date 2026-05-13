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

func TestRenderedContent_InsertsPreambleAfterFrontmatter(t *testing.T) {
	s := Skill{
		Name: "k8s-operations",
		Content: `---
name: k8s-operations
description: Use when investigating pod failures
triggers: [kubectl, k8s]
---

# K8s Operations

Use ` + "`run_k8s_cli`" + ` to investigate.
`,
	}
	rendered := s.RenderedContent()

	// Frontmatter still at top with the CLI install directory name.
	if !strings.HasPrefix(rendered, "---\nname: \"praxis-k8s-operations\"") {
		t.Errorf("frontmatter should still be at top, got prefix: %q", rendered[:80])
	}
	// Closing --- still present
	if !strings.Contains(rendered, "\n---\n") {
		t.Errorf("closing frontmatter fence missing")
	}
	// Preamble appears AFTER closing --- and BEFORE the body
	preambleIdx := strings.Index(rendered, "Execution context")
	closingFence := strings.Index(rendered, "\n---\n")
	bodyHeading := strings.Index(rendered, "# K8s Operations")
	if !(closingFence < preambleIdx && preambleIdx < bodyHeading) {
		t.Errorf(
			"order should be: closing-fence(%d) < preamble(%d) < body(%d)",
			closingFence, preambleIdx, bodyHeading)
	}
	// Original body content preserved verbatim
	if !strings.Contains(rendered, "Use `run_k8s_cli` to investigate.") {
		t.Errorf("original body missing")
	}
}

func TestRenderedContent_NoFrontmatter_PrependsPreamble(t *testing.T) {
	s := Skill{
		Name:        "plain",
		Description: "Plain skill description",
		Content:     "# Just a heading\n\nNo frontmatter here.\n",
	}
	rendered := s.RenderedContent()
	if !strings.HasPrefix(rendered, "---\nname: \"praxis-plain\"\ndescription: \"Plain skill description\"\n---\n") {
		t.Errorf("frontmatter should be synthesized at top, got: %q", rendered[:90])
	}
	preambleIdx := strings.Index(rendered, "Execution context")
	bodyHeading := strings.Index(rendered, "# Just a heading")
	if !(0 < preambleIdx && preambleIdx < bodyHeading) {
		t.Errorf("order should be frontmatter, preamble, body; got preamble=%d body=%d", preambleIdx, bodyHeading)
	}
	if !strings.Contains(rendered, "# Just a heading") {
		t.Errorf("original body missing")
	}
}

func TestRenderedContent_NoFrontmatter_FallsBackDescription(t *testing.T) {
	s := Skill{
		Name:        "plain",
		DisplayName: "Plain Display Name",
		Content:     "# Just a heading\n",
	}
	rendered := s.RenderedContent()
	if !strings.HasPrefix(rendered, "---\nname: \"praxis-plain\"\ndescription: \"Plain Display Name\"\n---\n") {
		t.Errorf("frontmatter should use display name fallback, got: %q", rendered[:90])
	}
}

func TestRenderedContent_ExistingFrontmatterNameMatchesInstallDirectory(t *testing.T) {
	s := Skill{
		Name: "x",
		Content: `---
name: x
description: d
---

# Body
`,
	}
	rendered := s.RenderedContent()
	if !strings.HasPrefix(rendered, "---\nname: \"praxis-x\"\ndescription: d\n---\n") {
		t.Errorf("frontmatter name should match install directory, got: %q", rendered[:80])
	}
	if !strings.Contains(rendered, "# Body") {
		t.Errorf("original body missing")
	}
}

func TestRenderedContent_MalformedFrontmatter_SynthesizesLoadableFrontmatter(t *testing.T) {
	s := Skill{
		Name:        "broken",
		Description: "Broken skill description",
		Content:     "---\nname: broken\n# missing closing fence\n",
	}
	rendered := s.RenderedContent()
	if !strings.HasPrefix(rendered, "---\nname: \"praxis-broken\"\ndescription: \"Broken skill description\"\n---\n") {
		t.Errorf("frontmatter should be synthesized at top, got: %q", rendered[:100])
	}
	if !strings.Contains(rendered, "---\nname: broken\n# missing closing fence") {
		t.Errorf("original malformed content should remain in body")
	}
}

func TestRenderedContent_PreservesBodyByteForByte(t *testing.T) {
	originalBody := `---
name: x
description: d
---

# Body

Some content with special chars: $foo ` + "`bar`" + ` and "quotes".

` + "```bash" + `
echo "code block"
` + "```" + `

End.
`
	s := Skill{Name: "x", Content: originalBody}
	rendered := s.RenderedContent()
	// The original body content (everything after the closing ---)
	// must appear verbatim in the rendered output.
	bodyStart := strings.Index(originalBody, "\n# Body")
	expectedBodyTail := originalBody[bodyStart:]
	if !strings.Contains(rendered, strings.TrimLeft(expectedBodyTail, "\n")) {
		t.Errorf("body not preserved verbatim")
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
