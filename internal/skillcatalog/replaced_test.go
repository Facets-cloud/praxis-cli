package skillcatalog

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReplacedBy(t *testing.T) {
	tests := []struct{ name, want string }{
		{"cloud-operations", "praxis-v1"},
		{"praxis-cloud-operations", "praxis-v1"},
		{"facets-blueprint", "raptor-v1"},
		{"praxis-praxis-dag", "praxis-v1"},
		{"my-org-skill", ""},
	}
	for _, tc := range tests {
		if got := ReplacedBy(tc.name); got != tc.want {
			t.Errorf("ReplacedBy(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
	praxis, raptor := 0, 0
	for _, c := range replacedBy {
		switch c {
		case "praxis-v1":
			praxis++
		case "raptor-v1":
			raptor++
		}
	}
	if praxis != 16 || raptor != 14 {
		t.Errorf("replaced skills = %d praxis + %d raptor, want 16 + 14 (agent-factory CONSOLIDATED_GLOBAL_SKILLS)", praxis, raptor)
	}
}

// Fetch sends the claimed capabilities and drops the GLOBAL skills they
// replace, also when the server returns them anyway.
func TestFetch_ClaimsAndDropsReplaced(t *testing.T) {
	bundle := `[
	  {"name":"cloud-operations","scope":"global","content":"x"},
	  {"name":"facets-blueprint","scope":"global","content":"x"},
	  {"name":"cloud-operations","scope":"organization","content":"org copy"},
	  {"name":"incident-notes","scope":"global","content":"x"}
	]`
	tests := []struct {
		caps      []string
		wantQuery string
		wantNames []string
	}{
		{nil, "", []string{"cloud-operations/global", "facets-blueprint/global", "cloud-operations/organization", "incident-notes/global"}},
		{[]string{"praxis-v1"}, "praxis-v1", []string{"facets-blueprint/global", "cloud-operations/organization", "incident-notes/global"}},
		{[]string{"raptor-v1", "praxis-v1", "junk"}, "praxis-v1,raptor-v1", []string{"cloud-operations/organization", "incident-notes/global"}},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprint(tc.caps), func(t *testing.T) {
			var gotQuery string
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotQuery = r.URL.Query().Get("consolidated")
				_, _ = w.Write([]byte(bundle))
			}))
			defer ts.Close()
			skills, err := Fetch(ts.URL, map[string]string{"Authorization": "Bearer t"}, tc.caps...)
			if err != nil {
				t.Fatal(err)
			}
			if gotQuery != tc.wantQuery {
				t.Errorf("consolidated = %q, want %q", gotQuery, tc.wantQuery)
			}
			var names []string
			for _, s := range skills {
				names = append(names, s.Name+"/"+s.Scope)
			}
			if fmt.Sprint(names) != fmt.Sprint(tc.wantNames) {
				t.Errorf("skills = %v, want %v", names, tc.wantNames)
			}
		})
	}
}
