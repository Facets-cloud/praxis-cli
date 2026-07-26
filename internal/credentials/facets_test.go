package credentials

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFacetsCreds(t *testing.T, body string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".facets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "credentials"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadFacetsProfile_DefaultProfile(t *testing.T) {
	writeFacetsCreds(t, `[default]
control_plane_url = https://root.console.facets.cloud
username = user@corp
token = pat_abc123

[acme]
control_plane_url = https://acme.console.facets.cloud
username = admin@acme
token = pat_xyz
`)
	url, user, tok, err := ReadFacetsProfile("")
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://root.console.facets.cloud" || user != "user@corp" || tok != "pat_abc123" {
		t.Errorf("got (%q,%q,%q)", url, user, tok)
	}
}

func TestReadFacetsProfile_NamedProfile(t *testing.T) {
	writeFacetsCreds(t, `[acme]
control_plane_url = https://acme.console.facets.cloud
username = admin@acme
token = pat_xyz
`)
	url, user, tok, err := ReadFacetsProfile("acme")
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://acme.console.facets.cloud" || user != "admin@acme" || tok != "pat_xyz" {
		t.Errorf("got (%q,%q,%q)", url, user, tok)
	}
}

func TestReadFacetsProfile_MissingProfile(t *testing.T) {
	writeFacetsCreds(t, "[default]\nusername = u\ntoken = t\n")
	if _, _, _, err := ReadFacetsProfile("nope"); err == nil {
		t.Error("want error for missing profile")
	}
}

func TestReadFacetsProfile_EmptyCredentials(t *testing.T) {
	writeFacetsCreds(t, "[default]\ncontrol_plane_url = https://x\n")
	if _, _, _, err := ReadFacetsProfile("default"); err == nil {
		t.Error("want error when username/token empty")
	}
}

func TestReadFacetsProfile_MissingFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, _, _, err := ReadFacetsProfile("default"); err == nil {
		t.Error("want error when file absent")
	}
}
