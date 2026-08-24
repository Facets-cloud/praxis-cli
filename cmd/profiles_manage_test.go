package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
)

func resetProfilesManageFlags(t *testing.T) {
	t.Helper()
	profilesRenameJSON, profilesRmJSON = false, false
	t.Cleanup(func() { profilesRenameJSON, profilesRmJSON = false, false })
}

func TestProfilesRename_HappyPath(t *testing.T) {
	isolateHome(t)
	resetProfilesManageFlags(t)
	seedProfile(t, "test-x", "https://cp.test", "tok")
	if err := credentials.SetActive("test-x"); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	profilesRenameCmd.SetOut(&buf)
	profilesRenameJSON = true
	if err := profilesRenameCmd.RunE(profilesRenameCmd, []string{"test-x", "acme-prod"}); err != nil {
		t.Fatalf("rename err: %v", err)
	}
	out := buf.String()
	for _, want := range []string{`"ok": true`, `"renamed_to": "acme-prod"`, `"active_pointer_updated": true`} {
		if !strings.Contains(out, want) {
			t.Errorf("rename output missing %q\nfull: %s", want, out)
		}
	}
	store, _ := credentials.Load()
	if _, ok := store["acme-prod"]; !ok {
		t.Error("renamed profile missing from store")
	}
	active, _ := credentials.ResolveActiveGlobal()
	if active.Name != "acme-prod" {
		t.Errorf("active = %s, want acme-prod", active.Name)
	}
}

func TestProfilesRename_MissingOldExitsUsage(t *testing.T) {
	isolateHome(t)
	resetProfilesManageFlags(t)
	exit := stubOsExit(t)

	var buf bytes.Buffer
	profilesRenameCmd.SetOut(&buf)
	profilesRenameJSON = true
	if err := profilesRenameCmd.RunE(profilesRenameCmd, []string{"ghost", "new"}); err == nil {
		t.Fatal("rename of missing profile succeeded")
	}
	if *exit != exitcode.Usage {
		t.Errorf("osExit code = %d, want %d (Usage)", *exit, exitcode.Usage)
	}
}

func TestProfilesRm_NonActive(t *testing.T) {
	isolateHome(t)
	resetProfilesManageFlags(t)
	seedProfile(t, "default", "https://cp.test", "tok")
	seedProfile(t, "stale", "https://old.test", "tok2")
	if err := credentials.SetActive("default"); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	profilesRmCmd.SetOut(&buf)
	profilesRmJSON = true
	if err := profilesRmCmd.RunE(profilesRmCmd, []string{"stale"}); err != nil {
		t.Fatalf("rm err: %v", err)
	}
	if !strings.Contains(buf.String(), `"removed": "stale"`) {
		t.Errorf("rm output missing removed marker: %s", buf.String())
	}
	store, _ := credentials.Load()
	if _, ok := store["stale"]; ok {
		t.Error("profile still in store after rm")
	}
	if _, ok := store["default"]; !ok {
		t.Error("unrelated profile vanished")
	}
}

func TestProfilesRm_ActiveIsRefused(t *testing.T) {
	isolateHome(t)
	resetProfilesManageFlags(t)
	seedProfile(t, "default", "https://cp.test", "tok")
	exit := stubOsExit(t)

	var buf bytes.Buffer
	profilesRmCmd.SetOut(&buf)
	profilesRmJSON = true
	if err := profilesRmCmd.RunE(profilesRmCmd, []string{"default"}); err == nil {
		t.Fatal("rm of active profile succeeded")
	}
	if *exit != exitcode.Usage {
		t.Errorf("osExit code = %d, want %d (Usage)", *exit, exitcode.Usage)
	}
	store, _ := credentials.Load()
	if _, ok := store["default"]; !ok {
		t.Error("active profile was deleted despite refusal")
	}
}

// $PRAXIS_PROFILE selects the deployment a session talks to; it must NOT
// redefine which profile is undeletable. Resolving the guard through the env
// override let `PRAXIS_PROFILE=other praxis profiles rm default` delete the
// profile the persisted pointer and the installed org skills still refer to.
func TestProfilesRm_EnvOverrideCannotUnprotectActive(t *testing.T) {
	isolateHome(t)
	resetProfilesManageFlags(t)
	seedProfile(t, "default", "https://cp.test", "tok")
	seedProfile(t, "other", "https://other.test", "tok2")
	if err := credentials.SetActive("default"); err != nil {
		t.Fatal(err)
	}
	t.Setenv(credentials.EnvProfile, "other")
	exit := stubOsExit(t)

	var buf bytes.Buffer
	profilesRmCmd.SetOut(&buf)
	profilesRmJSON = true
	if err := profilesRmCmd.RunE(profilesRmCmd, []string{"default"}); err == nil {
		t.Fatal("rm of the persisted active profile succeeded while PRAXIS_PROFILE named another")
	}
	if *exit != exitcode.Usage {
		t.Errorf("osExit code = %d, want %d (Usage)", *exit, exitcode.Usage)
	}
	store, _ := credentials.Load()
	if _, ok := store["default"]; !ok {
		t.Error("persisted active profile was deleted; its pointer and skills are now orphaned")
	}
}

// The other half: an override must not protect a profile either. `other` is
// not the persisted active, so naming it in the environment doesn't make it
// undeletable -- removing it can't desync pointer and skills.
func TestProfilesRm_EnvOverrideDoesNotProtectNonActive(t *testing.T) {
	isolateHome(t)
	resetProfilesManageFlags(t)
	seedProfile(t, "default", "https://cp.test", "tok")
	seedProfile(t, "other", "https://other.test", "tok2")
	if err := credentials.SetActive("default"); err != nil {
		t.Fatal(err)
	}
	t.Setenv(credentials.EnvProfile, "other")

	var buf bytes.Buffer
	profilesRmCmd.SetOut(&buf)
	profilesRmJSON = true
	if err := profilesRmCmd.RunE(profilesRmCmd, []string{"other"}); err != nil {
		t.Fatalf("rm of a non-active profile named by the env = %v, want success", err)
	}
	store, _ := credentials.Load()
	if _, ok := store["other"]; ok {
		t.Error("profile still present after removal")
	}
	if _, ok := store["default"]; !ok {
		t.Error("removal touched the active profile")
	}
}

func TestProfilesRm_MissingExitsUsage(t *testing.T) {
	isolateHome(t)
	resetProfilesManageFlags(t)
	seedProfile(t, "default", "https://cp.test", "tok")
	exit := stubOsExit(t)

	var buf bytes.Buffer
	profilesRmCmd.SetOut(&buf)
	profilesRmJSON = true
	if err := profilesRmCmd.RunE(profilesRmCmd, []string{"ghost"}); err == nil {
		t.Fatal("rm of missing profile succeeded")
	}
	if *exit != exitcode.Usage {
		t.Errorf("osExit code = %d, want %d (Usage)", *exit, exitcode.Usage)
	}
}
