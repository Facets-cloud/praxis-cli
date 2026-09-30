package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/agentcatalog"
	"github.com/Facets-cloud/praxis-cli/internal/harness"
	"github.com/Facets-cloud/praxis-cli/internal/skillcatalog"
)

func TestSetupPayloadMarksIncompleteSkillSync(t *testing.T) {
	payload := setupPayload("test", "tester", "https://test.invalid", "", false, postAuthState{skillWarning: "catalog fetch failed"})
	if payload["skill_sync_complete"] != false {
		t.Fatalf("partial setup looked complete: %v", payload)
	}
}

func TestRunPostAuthSetup_RealTreesNegotiateAndRetainRaptorFallback(t *testing.T) {
	for _, raptor := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing-raptor", true: "both-packages"}[raptor], func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			stubMCPManifestFetch(t)
			hosts := []harness.Harness{{Name: "claude-code", SkillDir: filepath.Join(t.TempDir(), "skills")}}
			oldDetect, oldFetch, oldAgents := detectHarnesses, fetchCatalog, fetchAgents
			detectHarnesses = func() []harness.Harness { return hosts }
			fetchCatalog = skillcatalog.Fetch
			fetchAgents = func(string, map[string]string) ([]agentcatalog.Agent, error) { return nil, nil }
			t.Cleanup(func() { detectHarnesses, fetchCatalog, fetchAgents = oldDetect, oldFetch, oldAgents })
			if raptor {
				root := filepath.Join(hosts[0].SkillDir, "raptor")
				mustMkdir(t, filepath.Join(root, "references"))
				if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("---\nname: raptor\ndescription: Canonical Raptor\nmetadata:\n  version: \"1.0\"\n---\nRead [route](references/route.md).\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "references/route.md"), []byte("Use the local CLI."), 0600); err != nil {
					t.Fatal(err)
				}
			}
			raptorNames := []string{"audit-facets-blueprint", "build-facets-module", "design-facets-module", "docs-helper", "facets-blueprint", "facets-ci", "facets-gcp-zero-change-import", "facets-notifications", "module-actions", "modules-repo-workflow", "release-debugging", "terraform-import", "zero-change-import", "facets-module-testing"}
			called := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				want := "praxis-v1"
				if raptor {
					want += ",raptor-v1"
				}
				if got := r.URL.Query().Get("consolidated"); got != want {
					t.Errorf("negotiated %q, want %q", got, want)
				}
				if _, err := os.Stat(filepath.Join(hosts[0].SkillDir, "praxis", "references/catalog-read.md")); err != nil {
					t.Errorf("advertised before complete install: %v", err)
				}
				skills := []skillcatalog.Skill{{Name: "cloud-operations", Scope: "global", Content: "old"}, {Name: "cloud-operations", Scope: "organization", Content: "organization instructions"}, {Name: "learning", Scope: "personal", Content: "personal instructions"}}
				for _, name := range raptorNames {
					skills = append(skills, skillcatalog.Skill{Name: name, Scope: "global", Content: "legacy raptor instructions"})
				}
				_ = json.NewEncoder(w).Encode(skills) // old server ignores capabilities
			}))
			defer server.Close()
			var out bytes.Buffer
			state := runPostAuthSetup(&out, false, server.URL, bearer("test"))
			if !called || state.skillWarning != "" {
				t.Fatalf("catalog integration failed: called=%v warning=%q output=%s", called, state.skillWarning, out.String())
			}
			for _, name := range raptorNames {
				_, err := os.Stat(filepath.Join(hosts[0].SkillDir, "praxis-"+name, "SKILL.md"))
				if !raptor && err != nil {
					t.Errorf("missing Raptor lost legacy %s: %v", name, err)
				}
				if raptor && !os.IsNotExist(err) {
					t.Errorf("verified Raptor left global %s: %v", name, err)
				}
			}
			for name, body := range map[string]string{"praxis-cloud-operations": "organization instructions", "praxis-learning": "personal instructions"} {
				b, err := os.ReadFile(filepath.Join(hosts[0].SkillDir, name, "SKILL.md"))
				if err != nil || !strings.Contains(string(b), body) {
					t.Errorf("lost scoped namesake %s: %s %v", name, b, err)
				}
			}
		})
	}
}
