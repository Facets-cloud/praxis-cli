package igcatalog

// Schema-conformance tests: decode JSON examples derived from the LIVE Praxis
// OpenAPI (captured verbatim in testdata/ig-openapi.json) into the Go types
// this client actually decodes, and assert the round-trip holds. This is the
// guard that would have caught every ig client/server drift before a human
// ran the binary:
//
//   - claims decoded a {git,catalogs} envelope as a bare []string,
//   - Catalog.Members decoded member objects as []string,
//   - Manifest dropped the required `catalog` field.
//
// The server is authoritative: these tests read its schema, never the other
// way round. Adding a new GET verb is cheap — capture a fresh openapi into
// testdata, add one row to conformanceCases, and the coverage guard below
// tells you if you forgot.

import (
	_ "embed"
	"encoding/json"
	"strings"
	"testing"
)

//go:embed testdata/ig-openapi.json
var igOpenAPIJSON []byte

// --- minimal OpenAPI shapes we assert against --------------------------

type openAPIDoc struct {
	Paths      map[string]map[string]openAPIOp `json:"paths"`
	Components struct {
		Schemas map[string]openAPISchema `json:"schemas"`
	} `json:"components"`
}

type openAPIOp struct {
	Responses map[string]struct {
		Content map[string]struct {
			Schema openAPISchema `json:"schema"`
		} `json:"content"`
	} `json:"responses"`
}

type openAPISchema struct {
	Ref        string                     `json:"$ref"`
	Type       string                     `json:"type"`
	Required   []string                   `json:"required"`
	Properties map[string]json.RawMessage `json:"properties"`
	Items      *openAPISchema             `json:"items"`
}

func loadOpenAPI(t *testing.T) openAPIDoc {
	t.Helper()
	var doc openAPIDoc
	if err := json.Unmarshal(igOpenAPIJSON, &doc); err != nil {
		t.Fatalf("parse embedded ig-openapi.json: %v", err)
	}
	if len(doc.Components.Schemas) == 0 {
		t.Fatalf("no component schemas in ig-openapi.json fixture")
	}
	return doc
}

func refName(s openAPISchema) string {
	if s.Ref != "" {
		return strings.TrimPrefix(s.Ref, "#/components/schemas/")
	}
	if s.Items != nil && s.Items.Ref != "" {
		return strings.TrimPrefix(s.Items.Ref, "#/components/schemas/")
	}
	return ""
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// --- the conformance table ---------------------------------------------
//
// One row per response type this client decodes. `example` is a full JSON
// object (every property populated) drawn from the live shapes; the test
// verifies it honors the schema's required set, decodes cleanly, and that
// every required field survives the decode into the Go type.
var conformanceCases = []struct {
	schema  string     // component schema name in the OpenAPI
	target  func() any // fresh pointer to the Go type the client decodes into
	example string     // full JSON example (every property populated)
}{
	{
		schema:  "IgCatalogSummary",
		target:  func() any { return new(Catalog) },
		example: `{"name":"capillary-cloud","version":"2026.07.10-104039","built_at":"2026-07-10T10:40:39Z","members":[{"name":"control-plane","kind":"code","git":"github.com/facets-cloud/control-plane","sha":"9a7c76c51a85da8dee3307bac95f85318961e8f9"}]}`,
	},
	{
		schema:  "IgMemberMeta",
		target:  func() any { return new(Member) },
		example: `{"name":"control-plane","kind":"code","git":"github.com/facets-cloud/control-plane","sha":"9a7c76c51a85da8dee3307bac95f85318961e8f9"}`,
	},
	{
		schema:  "IgClaimsResponse",
		target:  func() any { return new(claimsResponse) },
		example: `{"git":"github.com/facets-cloud/control-plane","catalogs":["capillary-cloud"]}`,
	},
	{
		schema:  "IgManifest",
		target:  func() any { return new(Manifest) },
		example: `{"catalog":"capillary-cloud","content":"name: capillary-cloud\nrepos: []\n","pushed_by":"a@b.com","pushed_at":"2026-07-10T10:40:39Z","git_sha":"9a7c76c51a85da8dee3307bac95f85318961e8f9"}`,
	},
}

// TestConformance_ResponseTypesMatchSchema is the core guard. For each response
// type the client decodes it proves three things against the LIVE schema:
//
//  1. a schema-shaped example decodes into the Go type without error,
//  2. every field the schema marks REQUIRED survives the decode (a Go type
//     that drops a required field, or decodes it empty, fails here), and
//  3. null optional fields and unknown future fields do not break the decode.
func TestConformance_ResponseTypesMatchSchema(t *testing.T) {
	doc := loadOpenAPI(t)
	for _, tc := range conformanceCases {
		t.Run(tc.schema, func(t *testing.T) {
			sch, ok := doc.Components.Schemas[tc.schema]
			if !ok {
				t.Fatalf("schema %q is not in the OpenAPI fixture", tc.schema)
			}

			// 0. The example must itself honor the schema's required set, so a
			//    lazy example can't paper over a dropped field.
			var exMap map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tc.example), &exMap); err != nil {
				t.Fatalf("example is not valid JSON: %v", err)
			}
			for _, req := range sch.Required {
				if _, ok := exMap[req]; !ok {
					t.Fatalf("example for %s omits required field %q; fix the fixture", tc.schema, req)
				}
			}

			// 1. The example decodes into the Go type without error.
			full := tc.target()
			if err := json.Unmarshal([]byte(tc.example), full); err != nil {
				t.Fatalf("decode example into %T: %v", full, err)
			}

			// 2. Round-trip: re-marshal and confirm every required field is
			//    still there and non-empty. A Go type missing the field
			//    (Manifest without `catalog`) drops it on re-marshal → caught.
			round, err := json.Marshal(full)
			if err != nil {
				t.Fatalf("re-marshal %T: %v", full, err)
			}
			var got map[string]json.RawMessage
			if err := json.Unmarshal(round, &got); err != nil {
				t.Fatalf("re-parse round-trip of %T: %v", full, err)
			}
			for _, req := range sch.Required {
				raw, present := got[req]
				if !present {
					t.Errorf("%s: required field %q is dropped by %T (add it to the Go type)", tc.schema, req, full)
					continue
				}
				if s := strings.TrimSpace(string(raw)); s == "null" || s == `""` {
					t.Errorf("%s: required field %q decoded empty (%s) in %T", tc.schema, req, s, full)
				}
			}

			// 3. Tolerance: every optional/nullable field set to null, plus an
			//    unknown future field, must still decode cleanly.
			tol := make(map[string]json.RawMessage, len(exMap)+1)
			for k, v := range exMap {
				tol[k] = v
			}
			for name := range sch.Properties {
				if !contains(sch.Required, name) {
					tol[name] = json.RawMessage("null")
				}
			}
			tol["__unknown_future_field__"] = json.RawMessage(`{"added":"later"}`)
			tolJSON, err := json.Marshal(tol)
			if err != nil {
				t.Fatalf("build tolerance example: %v", err)
			}
			if err := json.Unmarshal(tolJSON, tc.target()); err != nil {
				t.Errorf("%s: decode with null optionals + unknown field failed: %v\n%s", tc.schema, err, tolJSON)
			}
		})
	}
}

// TestConformance_EveryGETResponseTypeIsCovered makes the table hard to forget:
// every GET under /ig/ whose 200 body is a component schema (directly or as an
// array element) MUST have a conformanceCases row. Binary/empty-schema bodies
// (bundle download, member blob download) legitimately have no ref and are
// skipped. Add a GET verb, refresh the fixture, and this test names exactly
// which type you still owe a row.
func TestConformance_EveryGETResponseTypeIsCovered(t *testing.T) {
	doc := loadOpenAPI(t)
	covered := make(map[string]bool, len(conformanceCases))
	for _, tc := range conformanceCases {
		covered[tc.schema] = true
	}

	for path, methods := range doc.Paths {
		if !strings.Contains(path, "/ig/") {
			continue
		}
		op, ok := methods["get"]
		if !ok {
			continue
		}
		resp, ok := op.Responses["200"]
		if !ok {
			continue
		}
		media, ok := resp.Content["application/json"]
		if !ok {
			continue
		}
		name := refName(media.Schema)
		if name == "" {
			continue // binary or empty-schema body (bundle, member download)
		}
		if !covered[name] {
			t.Errorf("GET %s returns %s but no conformanceCases row covers it — add one", path, name)
		}
	}
}
