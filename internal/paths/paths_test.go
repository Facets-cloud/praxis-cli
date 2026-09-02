package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withHome temporarily redirects $HOME so the package's filesystem-derived
// helpers are deterministic.
func withHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func TestDir_BuildsUnderHome(t *testing.T) {
	home := withHome(t)
	got, err := Dir()
	if err != nil {
		t.Fatalf("Dir err = %v", err)
	}
	want := filepath.Join(home, ".praxis")
	if got != want {
		t.Errorf("Dir() = %q, want %q", got, want)
	}
}

func TestDir_NoHome(t *testing.T) {
	// Both HOME (Unix) and USERPROFILE (Windows) cleared so os.UserHomeDir()
	// errors. We don't assert on the exact error message — just that one is
	// returned and Dir doesn't paper over it.
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	if _, err := Dir(); err == nil {
		t.Fatal("Dir() should error when home is unresolvable, got nil")
	}
}

func TestCredentials_UnderDotPraxis(t *testing.T) {
	home := withHome(t)
	got, err := Credentials()
	if err != nil {
		t.Fatalf("Credentials err = %v", err)
	}
	want := filepath.Join(home, ".praxis", "credentials")
	if got != want {
		t.Errorf("Credentials() = %q, want %q", got, want)
	}
}

// We don't write to disk in tests — Credentials() returns a path; actual
// reads/writes happen in the cmd layer.
var _ = os.Remove

// mkProjectRoot writes dir/.facets/credentials — raptor's local-mode marker,
// which is what makes dir a project root — and returns the project root path
// (dir/.praxis) discovery is expected to report.
func mkProjectRoot(t *testing.T, dir string) string {
	t.Helper()
	facets := filepath.Join(dir, ".facets")
	if err := os.MkdirAll(facets, 0o700); err != nil {
		t.Fatalf("mkdir marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(facets, "credentials"), []byte("[default]\ncontrol_plane_url = https://x\nusername = u\ntoken = t\n"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	return filepath.Join(dir, ".praxis")
}

func TestProjectRoot_FoundInCwd(t *testing.T) {
	home := withHome(t)
	repo := filepath.Join(home, "work", "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := mkProjectRoot(t, repo)
	t.Cleanup(SetGetwdForTest(func() (string, error) { return repo, nil }))

	got, ok, err := ProjectRoot()
	if err != nil || !ok {
		t.Fatalf("ProjectRoot() = %q, ok=%v, err=%v; want found", got, ok, err)
	}
	if got != marker {
		t.Errorf("ProjectRoot() = %q, want %q", got, marker)
	}
}

func TestProjectRoot_FoundInAncestor(t *testing.T) {
	home := withHome(t)
	repo := filepath.Join(home, "work", "repo")
	sub := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := mkProjectRoot(t, repo)
	t.Cleanup(SetGetwdForTest(func() (string, error) { return sub, nil }))

	got, ok, _ := ProjectRoot()
	if !ok || got != marker {
		t.Errorf("ProjectRoot() = %q, ok=%v; want %q (walk up to ancestor)", got, ok, marker)
	}
}

func TestProjectRoot_StopsBelowHome_NeverReturnsHomeDotPraxis(t *testing.T) {
	home := withHome(t)
	// The home store exists, and cwd is a plain dir under home with no
	// project marker. Discovery must NOT mistake the home file for a project.
	mkProjectRoot(t, home) // creates ~/.facets/credentials
	repo := filepath.Join(home, "work", "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(SetGetwdForTest(func() (string, error) { return repo, nil }))

	if got, ok, _ := ProjectRoot(); ok {
		t.Errorf("ProjectRoot() = %q, ok=true; want not-found (must not return ~/.praxis)", got)
	}
}

func TestProjectRoot_CwdOutsideHome_NotFound(t *testing.T) {
	withHome(t)
	outside := t.TempDir() // a sibling temp dir, not under the faked home
	mkProjectRoot(t, outside)
	t.Cleanup(SetGetwdForTest(func() (string, error) { return outside, nil }))

	if got, ok, _ := ProjectRoot(); ok {
		t.Errorf("ProjectRoot() = %q, ok=true; want not-found for cwd outside home", got)
	}
}

func TestActiveRoot_ProjectThenHomeThenOverride(t *testing.T) {
	home := withHome(t)
	homeRoot := filepath.Join(home, ".praxis")
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}

	// No project marker → home root.
	t.Cleanup(SetGetwdForTest(func() (string, error) { return repo, nil }))
	if got, _ := ActiveRoot(); got != homeRoot {
		t.Errorf("ActiveRoot() with no project = %q, want %q", got, homeRoot)
	}

	// Project marker present → project root.
	marker := mkProjectRoot(t, repo)
	if got, _ := ActiveRoot(); got != marker {
		t.Errorf("ActiveRoot() with project = %q, want %q", got, marker)
	}

	// Override wins over everything.
	restore := OverrideActiveRoot(homeRoot)
	if !RootIsPinned() {
		t.Error("RootIsPinned() = false after OverrideActiveRoot")
	}
	if got, _ := ActiveRoot(); got != homeRoot {
		t.Errorf("ActiveRoot() with override = %q, want %q", got, homeRoot)
	}
	restore()
	if RootIsPinned() {
		t.Error("RootIsPinned() = true after restore")
	}
}

func TestInstalledAndMCPTools_FollowActiveRoot(t *testing.T) {
	home := withHome(t)
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := mkProjectRoot(t, repo)
	t.Cleanup(SetGetwdForTest(func() (string, error) { return repo, nil }))

	inst, _ := Installed()
	if want := filepath.Join(marker, "installed.json"); inst != want {
		t.Errorf("Installed() = %q, want %q (project-local)", inst, want)
	}
	mcp, _ := MCPTools()
	if want := filepath.Join(marker, "mcp-tools.json"); mcp != want {
		t.Errorf("MCPTools() = %q, want %q (project-local)", mcp, want)
	}
}

func TestCredentials_AlwaysHome_EvenInProject(t *testing.T) {
	home := withHome(t)
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	mkProjectRoot(t, repo)
	t.Cleanup(SetGetwdForTest(func() (string, error) { return repo, nil }))

	got, _ := Credentials()
	if want := filepath.Join(home, ".praxis", "credentials"); got != want {
		t.Errorf("Credentials() = %q, want %q (must stay global even in project)", got, want)
	}
}

func TestEnsureProjectRoot_CreatesUnderHome(t *testing.T) {
	home := withHome(t)
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(SetGetwdForTest(func() (string, error) { return repo, nil }))

	root, err := EnsureProjectRoot()
	if err != nil {
		t.Fatalf("EnsureProjectRoot() err = %v", err)
	}
	if want := filepath.Join(repo, ".praxis"); root != want {
		t.Errorf("EnsureProjectRoot() = %q, want %q", root, want)
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		t.Errorf("EnsureProjectRoot() did not create dir: %v", err)
	}
}

func TestEnsureProjectRoot_RejectsOutsideHome(t *testing.T) {
	withHome(t)
	outside := t.TempDir()
	t.Cleanup(SetGetwdForTest(func() (string, error) { return outside, nil }))

	_, err := EnsureProjectRoot()
	if err == nil {
		t.Fatal("EnsureProjectRoot() should error for cwd outside home, got nil")
	}
	// The message carries the user-facing guidance — assert on it, not just non-nil.
	if !strings.Contains(err.Error(), "under your home directory") {
		t.Errorf("error should explain the home-subtree requirement; got %q", err.Error())
	}
}

func TestEnsureProjectRoot_RejectsHomeItself(t *testing.T) {
	home := withHome(t)
	t.Cleanup(SetGetwdForTest(func() (string, error) { return home, nil }))

	_, err := EnsureProjectRoot()
	if err == nil {
		t.Fatal("EnsureProjectRoot() should error when cwd is home itself, got nil")
	}
	if !strings.Contains(err.Error(), "under your home directory") {
		t.Errorf("error should explain the home-subtree requirement; got %q", err.Error())
	}
}

// symlinkedHome builds the macOS /tmp layout in a temp dir:
//
//	<base>/phys/         the real directory
//	<base>/link -> phys  a symlink to it, like /tmp -> /private/tmp
//
// It returns (link, phys) — two spellings of one directory.
func symlinkedHome(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	phys := filepath.Join(base, "phys")
	if err := os.MkdirAll(phys, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(phys, link); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}
	return link, phys
}

// $HOME is logical (macOS /tmp/x) while os.Getwd() reports the physical path.
// A plain prefix test calls that "outside home" and silently disables local
// mode for every command.
func TestProjectRoot_LogicalHomePhysicalCwd(t *testing.T) {
	link, phys := symlinkedHome(t)
	t.Setenv("HOME", link)
	repo := filepath.Join(phys, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	mkProjectRoot(t, repo)
	t.Cleanup(SetGetwdForTest(func() (string, error) { return repo, nil }))

	got, ok, err := ProjectRoot()
	if err != nil || !ok {
		t.Fatalf("ProjectRoot() = %q, ok=%v, err=%v; want found through the symlinked home", got, ok, err)
	}
	if want := filepath.Join(resolved(repo), dirName); got != want {
		t.Errorf("ProjectRoot() = %q, want %q", got, want)
	}
}

// The mirror image: $PWD carries a logical path into a physical $HOME.
func TestProjectRoot_PhysicalHomeLogicalCwd(t *testing.T) {
	link, phys := symlinkedHome(t)
	t.Setenv("HOME", phys)
	if err := os.MkdirAll(filepath.Join(phys, "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	mkProjectRoot(t, filepath.Join(phys, "repo"))
	logicalRepo := filepath.Join(link, "repo")
	t.Cleanup(SetGetwdForTest(func() (string, error) { return logicalRepo, nil }))

	got, ok, err := ProjectRoot()
	if err != nil || !ok {
		t.Fatalf("ProjectRoot() = %q, ok=%v, err=%v; want found", got, ok, err)
	}
	if want := filepath.Join(resolved(logicalRepo), dirName); got != want {
		t.Errorf("ProjectRoot() = %q, want %q", got, want)
	}
}

// The trap in a naive fix: resolve the cwd but keep comparing it to the
// unresolved home and the `dir != home` bound never matches, so the walk
// climbs past home to / and can adopt any stray .praxis it passes. Aligning
// BOTH sides is what keeps the bound real.
func TestProjectRoot_SymlinkedHome_WalkStillStopsAtHome(t *testing.T) {
	link, phys := symlinkedHome(t)
	t.Setenv("HOME", link)
	stray := mkProjectRoot(t, filepath.Dir(phys)) // ABOVE home
	repo := filepath.Join(phys, "work", "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(SetGetwdForTest(func() (string, error) { return repo, nil }))

	if got, ok, _ := ProjectRoot(); ok {
		t.Errorf("ProjectRoot() = %q, ok=true; want not-found — the walk escaped home and picked up %q", got, stray)
	}
}

// Resolution must only ever ADD matches: a directory reached through a symlink
// that points OUT of home worked before (plain prefix on the logical path) and
// must keep working, with the spelling the user typed.
func TestProjectRoot_SymlinkOutOfHome_StillDiscovered(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	outside := filepath.Join(base, "elsewhere")
	if err := os.MkdirAll(filepath.Join(outside, "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, "linked")); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}
	mkProjectRoot(t, filepath.Join(outside, "repo"))
	t.Setenv("HOME", home)
	logical := filepath.Join(home, "linked", "repo")
	t.Cleanup(SetGetwdForTest(func() (string, error) { return logical, nil }))

	got, ok, err := ProjectRoot()
	if err != nil || !ok {
		t.Fatalf("ProjectRoot() = %q, ok=%v, err=%v; want found (no new rejection)", got, ok, err)
	}
	if want := filepath.Join(logical, dirName); got != want {
		t.Errorf("ProjectRoot() = %q, want %q — a match on the paths as given keeps their spelling", got, want)
	}
}

// What EnsureProjectRoot creates must be what ProjectRoot later finds once the
// credentials marker is written beside it; otherwise `--local` pins a tree
// that discovery can't see.
func TestEnsureProjectRoot_SymlinkedHome_RoundTripsWithDiscovery(t *testing.T) {
	link, phys := symlinkedHome(t)
	t.Setenv("HOME", link)
	repo := filepath.Join(phys, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(SetGetwdForTest(func() (string, error) { return repo, nil }))
	mkProjectRoot(t, repo)

	created, err := EnsureProjectRoot()
	if err != nil {
		t.Fatalf("EnsureProjectRoot() err = %v; want success under a symlinked home", err)
	}
	if fi, statErr := os.Stat(created); statErr != nil || !fi.IsDir() {
		t.Fatalf("EnsureProjectRoot() = %q but it's not a directory: %v", created, statErr)
	}
	found, ok, _ := ProjectRoot()
	if !ok || found != created {
		t.Errorf("ProjectRoot() = %q, ok=%v; want the just-created %q", found, ok, created)
	}
}

// cwd is home itself, spelled physically while $HOME is the symlink. Aligning
// makes them compare equal, so the refusal names the real reason instead of
// claiming the home directory is outside the home directory.
func TestEnsureProjectRoot_SymlinkedHomeItself_Rejected(t *testing.T) {
	link, phys := symlinkedHome(t)
	t.Setenv("HOME", link)
	t.Cleanup(SetGetwdForTest(func() (string, error) { return phys, nil }))

	_, err := EnsureProjectRoot()
	if err == nil {
		t.Fatal("EnsureProjectRoot() should refuse the home directory itself, got nil")
	}
	if !strings.Contains(err.Error(), "not the home directory itself") {
		t.Errorf("error should say cwd IS home, not that it's outside it; got %q", err.Error())
	}
}

func TestAlignUnder(t *testing.T) {
	link, phys := symlinkedHome(t)
	outs := t.TempDir()
	// The canonical spelling of phys. The cases below that mix a resolvable
	// base with an unresolvable path MUST anchor on this: $TMPDIR is itself
	// symlinked on macOS (/var -> /private/var) but canonical on Linux, so a
	// case built on the raw phys would assert one thing locally and the
	// opposite in CI.
	physCanon, err := filepath.EvalSymlinks(phys)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(phys, "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(phys, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		base, path string
		wantOK     bool
		// wantResolved: the returned pair must be symlink-resolved (the second
		// attempt matched) rather than the strings as given.
		wantResolved bool
	}{
		{name: "same path", base: phys, path: phys, wantOK: true},
		{name: "plain descendant", base: phys, path: filepath.Join(phys, "a", "b"), wantOK: true},
		{name: "trailing slash and dots", base: phys + "/", path: filepath.Join(phys, "a", "..", "a"), wantOK: true},
		{name: "logical base, physical path", base: link, path: filepath.Join(phys, "repo"), wantOK: true, wantResolved: true},
		{name: "physical base, logical path", base: phys, path: filepath.Join(link, "repo"), wantOK: true, wantResolved: true},
		{name: "genuinely outside", base: phys, path: outs, wantOK: false},
		{name: "sibling prefix is not a descendant", base: phys, path: phys + "-other", wantOK: false},
		// A missing leaf still aligns: the base resolves, and the path sits
		// literally beneath the resolved base. Containment is asked BEFORE
		// mkdir (see TestResolved_MissingPathIsUnchanged), so reporting
		// "outside" for a directory about to be created would resurrect a
		// variant of the bug this function exists to fix.
		{name: "missing leaf under a symlinked base still aligns", base: link, path: filepath.Join(physCanon, "ghost"), wantOK: true, wantResolved: true},
		// With nothing resolvable on either side the literal compare is all
		// there is — and it must still reject a genuine outsider.
		{name: "unresolvable and outside stays a miss", base: filepath.Join(outs, "nope"), path: filepath.Join(physCanon, "ghost"), wantOK: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotBase, gotPath, ok := alignUnder(tc.base, tc.path)
			if ok != tc.wantOK {
				t.Fatalf("alignUnder(%q, %q) ok = %v, want %v", tc.base, tc.path, ok, tc.wantOK)
			}
			if !ok {
				// Failure reports the paths as given, so error messages name
				// what the user typed.
				if gotBase != filepath.Clean(tc.base) || gotPath != filepath.Clean(tc.path) {
					t.Errorf("on failure got (%q, %q), want the inputs as given", gotBase, gotPath)
				}
				return
			}
			// Whichever namespace matched, the pair must agree — that's what
			// lets a caller walk from path up to base.
			if !isUnder(gotBase, gotPath) {
				t.Errorf("returned pair (%q, %q) is not self-consistent", gotBase, gotPath)
			}
			// Assert BOTH halves of the pair, not just the base: alignUnder
			// returns the path too, callers walk up from it, and a base-only
			// assertion would pass even if the path came back from the other
			// namespace.
			if tc.wantResolved {
				if gotBase != resolved(tc.base) {
					t.Errorf("base = %q, want the resolved %q", gotBase, resolved(tc.base))
				}
				if gotPath != resolved(tc.path) {
					t.Errorf("path = %q, want the resolved %q", gotPath, resolved(tc.path))
				}
			} else {
				if gotBase != filepath.Clean(tc.base) {
					t.Errorf("base = %q, want the input as given %q (no needless resolution)", gotBase, filepath.Clean(tc.base))
				}
				if gotPath != filepath.Clean(tc.path) {
					t.Errorf("path = %q, want the input as given %q (no needless resolution)", gotPath, filepath.Clean(tc.path))
				}
			}
		})
	}
}

func TestResolved_MissingPathIsUnchanged(t *testing.T) {
	// EvalSymlinks fails on a path that doesn't exist; containment checks that
	// run before mkdir still need an answer.
	missing := filepath.Join(t.TempDir(), "nope", "deeper")
	if got := resolved(missing); got != missing {
		t.Errorf("resolved(%q) = %q, want it unchanged", missing, got)
	}
}

func TestProjectRoot_BarePraxisDirIsNotLocalMode(t *testing.T) {
	// A committed <repo>/.praxis (receipt, snapshot, or an old pointer) is not
	// an opt-in. Only the credentials file beside it is.
	home := withHome(t)
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".praxis"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".praxis", "config.json"), []byte("[default]\nprofile = acme\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(SetGetwdForTest(func() (string, error) { return repo, nil }))
	if got, ok, _ := ProjectRoot(); ok {
		t.Errorf("ProjectRoot() = %q, ok=true; a bare .praxis must be inert", got)
	}
	path, profile, ok := LegacyProjectPointer()
	if !ok || profile != "acme" || path != filepath.Join(repo, ".praxis", "config.json") {
		t.Errorf("LegacyProjectPointer() = %q, %q, %v", path, profile, ok)
	}
	// With the marker present the tree is real local mode, and the old pointer
	// is not reported.
	mkProjectRoot(t, repo)
	if _, _, ok := LegacyProjectPointer(); ok {
		t.Error("LegacyProjectPointer() reported a pointer inside a real local tree")
	}
}
