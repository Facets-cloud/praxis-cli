package skillcatalog

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestConsolidated_CompatibleServerMatrix(t *testing.T) {
	for _, modern := range []bool{false, true} {
		for _, tc := range []struct {
			name      string
			caps      []string
			wantQuery string
			want      []string
		}{
			{"legacy", nil, "", []string{"global/cloud-operations", "global/release-debugging", "organization/cloud-operations", "personal/release-debugging", "/cloud-operations", "global/cloud-operations-extra"}},
			{"praxis", []string{"praxis-v1"}, "praxis-v1", []string{"global/release-debugging", "organization/cloud-operations", "personal/release-debugging", "/cloud-operations", "global/cloud-operations-extra"}},
			{"both", []string{"praxis-v1", "raptor-v1"}, "praxis-v1,raptor-v1", []string{"organization/cloud-operations", "personal/release-debugging", "/cloud-operations", "global/cloud-operations-extra"}},
			{"unknown", []string{"unverified-v2"}, "", []string{"global/cloud-operations", "global/release-debugging", "organization/cloud-operations", "personal/release-debugging", "/cloud-operations", "global/cloud-operations-extra"}},
		} {
			t.Run(tc.name+map[bool]string{true: "/new-server", false: "/old-server"}[modern], func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if got := r.URL.Query().Get("consolidated"); got != tc.wantQuery {
						t.Errorf("consolidated=%q, want %q", got, tc.wantQuery)
					}
					if r.Header.Get("Authorization") != "Bearer test" {
						t.Error("lost auth")
					}
					skills := []Skill{{Name: "cloud-operations", Scope: "global"}, {Name: "release-debugging", Scope: "global"}, {Name: "cloud-operations", Scope: "organization"}, {Name: "release-debugging", Scope: "personal"}, {Name: "cloud-operations"}, {Name: "cloud-operations-extra", Scope: "global"}}
					if modern && r.URL.Query().Get("consolidated") == "praxis-v1,raptor-v1" {
						skills = skills[2:]
					}
					_ = json.NewEncoder(w).Encode(skills)
				}))
				defer server.Close()
				got, err := Fetch(server.URL, map[string]string{"Authorization": "Bearer test"}, tc.caps...)
				if err != nil {
					t.Fatal(err)
				}
				var names []string
				for _, s := range got {
					names = append(names, s.Scope+"/"+s.Name)
				}
				if !reflect.DeepEqual(names, tc.want) {
					t.Errorf("fallback scope filtering: got %v want %v", names, tc.want)
				}
			})
		}
	}
}
