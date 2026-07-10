package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
	"github.com/Facets-cloud/praxis-cli/internal/igcatalog"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/spf13/cobra"
)

// `praxis ig` is the ONLY client of the ig catalog server. Repo CI,
// laptops, and humans call this CLI; the `ig` binary itself never learns
// servers exist — it reads the filesystem that `praxis ig sync`
// materializes under $IG_HOME. praxis fetches; ig reads. This file is the
// half that talks to the network so that ig never has to.
//
// The noun is `ig` (not `catalog`) because praxis already uses "catalog"
// to mean the skill catalog (`praxis login` syncs the org skill catalog).

const igDefaultHome = ".ig"

// errBundleMismatch marks a downloaded bundle whose bytes don't hash to
// the server-supplied digest. sync refuses to replace the live tree and
// exits non-zero — CI depends on this.
var errBundleMismatch = errors.New("bundle digest mismatch")

// errBundleContract marks a downloaded bundle whose extracted tree violates
// the archive contract: metadata.json MUST sit at the tree root. sync
// refuses to swap such a tree into place and exits non-zero. Silently
// tolerating a malformed bundle is exactly what once materialized a broken,
// double-nested catalog (projects/<c>/<c>/metadata.json) that `ig status`
// then reported as NO_METADATA while praxis claimed success.
var errBundleContract = errors.New("bundle contract violation")

// maxMemberFileBytes bounds a single extracted file so a malformed or
// hostile bundle can't exhaust the disk. Catalog members are JSON graphs;
// this is a generous ceiling, not a normal size.
const maxMemberFileBytes = 1 << 30 // 1 GiB

var (
	igJSON    bool
	igProfile string

	igSyncAll bool

	igPublishCatalog string
	igPublishMember  string
	igPublishGit     string
	igPublishSha     string

	igClaimsGit string

	igManifestCatalog string
	igManifestOut     string
)

// syncState is the .sync.json handshake praxis WRITES and ig READS. ig
// never writes one; it only reads `refresh` (echoed verbatim as the fix on
// its CATALOG_SYNC_STALE issue) and `from` (opaque provenance it displays
// but never parses).
type syncState struct {
	SyncedAt string `json:"synced_at"`
	Digest   string `json:"digest"`
	From     string `json:"from"`
	Refresh  string `json:"refresh"`
}

// memberMeta is the optional <dir>/member/<m>/member-meta.json that
// carries a member's canonical git URL and commit sha, so `publish`
// doesn't need --git/--sha flags when CI already wrote the meta.
type memberMeta struct {
	Git string `json:"git"`
	Sha string `json:"sha"`
}

// gitHeadSHA is a seam (tests stub it) that returns the HEAD sha of the
// working tree a manifest file came from. Shelling out to `git` is fine —
// only shelling out to `ig` is forbidden.
var gitHeadSHA = func(dir string) (string, error) {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// nowFn is a seam for the sync timestamp.
var nowFn = time.Now

// --- $IG_HOME layout ---------------------------------------------------

func igHome() (string, error) {
	if h := os.Getenv("IG_HOME"); h != "" {
		return h, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, igDefaultHome), nil
}

func catalogDir(home, catalog string) string {
	return filepath.Join(home, "projects", catalog)
}

func readSyncState(dir string) (syncState, bool) {
	data, err := os.ReadFile(filepath.Join(dir, ".sync.json"))
	if err != nil {
		return syncState{}, false
	}
	var s syncState
	if json.Unmarshal(data, &s) != nil {
		return syncState{}, false
	}
	return s, true
}

func writeSyncState(dir string, s syncState) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ".sync.json"), append(data, '\n'), 0o644)
}

// syncedCatalogs lists the catalogs already materialized under $IG_HOME
// (those with a .sync.json), sorted, for `status` with no argument.
func syncedCatalogs(home string) []string {
	entries, err := os.ReadDir(filepath.Join(home, "projects"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, ok := readSyncState(filepath.Join(home, "projects", e.Name())); ok {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// --- the linchpin: refresh composition ---------------------------------

// composeRefresh is the exact command that reproduces this sync. ig echoes
// it verbatim as the fix on CATALOG_SYNC_STALE — ig composes nothing, so
// this MUST include `--profile <p>` whenever the active profile is not the
// default. That is what keeps ig ignorant of praxis: it relays a string it
// was handed rather than building one.
func composeRefresh(catalog, profileName string) string {
	cmd := "praxis ig sync " + catalog
	if profileName != "" && profileName != credentials.DefaultProfileName {
		cmd += " --profile " + profileName
	}
	return cmd
}

// --- sync core (testable; never calls os.Exit) -------------------------

// syncOne downloads a catalog's bundle (conditional on the local digest),
// verifies it, and atomically materializes it under $IG_HOME, writing
// .sync.json beside it. Returns upToDate=true when the server answered 304
// (no re-extract). Never calls os.Exit — the cmd layer maps err to a code.
func syncOne(active credentials.Active, catalog string) (upToDate bool, err error) {
	home, err := igHome()
	if err != nil {
		return false, err
	}
	dir := catalogDir(home, catalog)
	local, _ := readSyncState(dir)

	body, etag, notModified, err := igcatalog.DownloadBundle(
		active.Profile.URL, active.Profile.Token, catalog, local.Digest)
	if err != nil {
		return false, err
	}
	if notModified {
		return true, nil
	}
	if err := verifyDigest(body, etag); err != nil {
		return false, err
	}

	if err := os.MkdirAll(home, 0o755); err != nil {
		return false, err
	}
	tmp, err := os.MkdirTemp(home, ".sync-"+sanitizeTemp(catalog)+"-*")
	if err != nil {
		return false, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(tmp)
		}
	}()

	if err := extractTarGz(body, tmp); err != nil {
		return false, err
	}
	// Enforce the archive contract on the freshly-extracted tree BEFORE any
	// swap: metadata.json MUST sit at the root. A malformed bundle fails
	// here, the deferred cleanup drops the temp dir, and the live tree at
	// `dir` is never touched.
	if err := validateBundleTree(tmp, catalog); err != nil {
		return false, err
	}
	state := syncState{
		SyncedAt: nowFn().UTC().Format(time.RFC3339),
		Digest:   etag,
		From:     fmt.Sprintf("praxis@%s:%s@%s", active.Name, catalog, etag),
		Refresh:  composeRefresh(catalog, active.Name),
	}
	if err := writeSyncState(tmp, state); err != nil {
		return false, err
	}
	if err := swapDir(tmp, dir); err != nil {
		return false, err
	}
	committed = true
	return false, nil
}

// verifyDigest confirms the downloaded bytes hash to the server-supplied
// digest. Only sha256: digests are verifiable; any other opaque validator
// is trusted (the server is authoritative for its own ETag semantics).
func verifyDigest(body []byte, etag string) error {
	if !strings.HasPrefix(etag, "sha256:") {
		return nil
	}
	want := strings.TrimPrefix(etag, "sha256:")
	sum := sha256.Sum256(body)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("%w: server said %s but downloaded bytes hash to sha256:%s", errBundleMismatch, etag, got)
	}
	return nil
}

// swapDir atomically moves the freshly-extracted tmp tree into place. The
// previous tree is renamed aside first and restored if the final rename
// fails, so a crash mid-swap never leaves a half-extracted catalog and
// never loses the old one.
func swapDir(tmp, dir string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	var backup string
	if _, err := os.Stat(dir); err == nil {
		backup = dir + ".bak-" + fmt.Sprint(nowFn().UnixNano())
		if err := os.Rename(dir, backup); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp, dir); err != nil {
		if backup != "" {
			_ = os.Rename(backup, dir) // restore the previous tree
		}
		return err
	}
	if backup != "" {
		_ = os.RemoveAll(backup)
	}
	return nil
}

// extractTarGz unpacks a gzipped tarball into dest, guarding against
// path-traversal ("zip slip") and oversized entries.
func extractTarGz(data []byte, dest string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("gunzip bundle: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read bundle tar: %w", err)
		}
		clean := filepath.Clean(hdr.Name)
		if clean == "." || clean == "" {
			continue
		}
		target := filepath.Join(dest, clean)
		if !withinDir(dest, target) {
			return fmt.Errorf("bundle entry %q escapes the destination directory", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := writeTarFile(tr, target, hdr); err != nil {
				return err
			}
		default:
			// Skip symlinks, devices, etc. — a catalog bundle is plain files.
		}
	}
	return nil
}

func writeTarFile(tr *tar.Reader, target string, hdr *tar.Header) error {
	mode := os.FileMode(hdr.Mode).Perm()
	if mode == 0 {
		mode = 0o644
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(tr, maxMemberFileBytes+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n > maxMemberFileBytes {
		return fmt.Errorf("bundle entry %q exceeds %d bytes", hdr.Name, maxMemberFileBytes)
	}
	return nil
}

func withinDir(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// validateBundleTree enforces the ig archive contract on a freshly-extracted
// tree, run BEFORE the tree is swapped into place. The bundle must carry the
// catalog directory's *contents* — metadata.json at the archive root, plus
// member/<m>/graphify-out/graph.json and catalog/graphify-out/graph.json — so
// a valid tree always has metadata.json at its root.
//
// The failure we hit in production: the server emitted entries prefixed with
// the catalog name (`<catalog>/metadata.json`, ...), which extracts to
// `<catalog>/metadata.json` and, once swapped in, becomes the double-nested
// projects/<catalog>/<catalog>/metadata.json that `ig status` calls
// NO_METADATA. We diagnose that exact shape specifically.
//
// We deliberately do NOT strip the prefix to "fix it up": silently tolerating
// a malformed bundle is precisely what let the broken tree ship and would
// hide the next server regression. Fail loudly instead.
func validateBundleTree(dir, catalog string) error {
	if isRegularFile(filepath.Join(dir, "metadata.json")) {
		return nil
	}
	// The exact shape of the bug: the tree is rooted at a single directory
	// named after the catalog, with metadata.json one level down.
	if isRegularFile(filepath.Join(dir, catalog, "metadata.json")) {
		return fmt.Errorf("%w: bundle for catalog %q has no metadata.json at its root; it appears to be rooted at %s/ instead, so the server is emitting a catalog-prefixed archive. Expected the catalog directory's contents (metadata.json at the archive root), not a %s/ directory",
			errBundleContract, catalog, catalog, catalog)
	}
	return fmt.Errorf("%w: bundle for catalog %q has no metadata.json at its root; the archive does not match the ig catalog contract",
		errBundleContract, catalog)
}

// isRegularFile reports whether path is an existing regular file (not a
// directory or other node).
func isRegularFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

// sanitizeTemp keeps the temp-dir suffix filesystem-safe for catalog names
// that contain slashes or other separators.
func sanitizeTemp(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, s)
}

// --- status core (no bundle download) ----------------------------------

// statusOne compares the local .sync.json digest against the server's
// current version WITHOUT downloading the bundle. state is one of
// "up to date" | "stale" | "not synced".
func statusOne(active credentials.Active, catalog string) (state, serverVersion, localDigest string, err error) {
	home, err := igHome()
	if err != nil {
		return "", "", "", err
	}
	local, synced := readSyncState(catalogDir(home, catalog))
	c, err := igcatalog.GetCatalog(active.Profile.URL, active.Profile.Token, catalog)
	if err != nil {
		return "", "", "", err
	}
	switch {
	case !synced:
		state = "not synced"
	case local.Digest == c.Version:
		state = "up to date"
	default:
		state = "stale"
	}
	return state, c.Version, local.Digest, nil
}

// --- auth resolution honoring --profile --------------------------------

// activeOrAuthExitProfile resolves the credentials profile (honoring the
// --profile flag) or exits with the auth code. Variant of memory.go's
// activeOrAuthExit that threads the flag so a non-default profile is
// selectable — and reproducible in .sync.json's refresh.
func activeOrAuthExitProfile(out io.Writer, profileFlag string) credentials.Active {
	active, err := credentials.ResolveActive(profileFlag)
	if err != nil {
		render.PrintError(out, true, err.Error(), "could not load credentials", exitcode.Error)
		os.Exit(exitcode.Error)
	}
	if !active.Loaded || active.Profile.Token == "" {
		render.PrintError(out, true,
			fmt.Sprintf("no credentials for profile %q", active.Name),
			"run `praxis login` (or `praxis login --profile "+active.Name+"`)",
			exitcode.Auth)
		os.Exit(exitcode.Auth)
	}
	return active
}

// --- command tree ------------------------------------------------------

func init() {
	igCmd.PersistentFlags().BoolVar(&igJSON, "json", false, "JSON output (default when stdout is non-TTY)")
	igCmd.PersistentFlags().StringVar(&igProfile, "profile", "", "credentials profile to use (defaults to the active profile)")

	igSyncCmd.Flags().BoolVar(&igSyncAll, "all", false, "sync every catalog in the org")

	igPublishCmd.Flags().StringVar(&igPublishCatalog, "catalog", "", "target catalog (required)")
	igPublishCmd.Flags().StringVar(&igPublishMember, "member", "", "member name (required)")
	igPublishCmd.Flags().StringVar(&igPublishGit, "git", "", "member's canonical git URL (overrides member-meta.json)")
	igPublishCmd.Flags().StringVar(&igPublishSha, "sha", "", "member's commit sha (overrides member-meta.json)")

	igClaimsCmd.Flags().StringVar(&igClaimsGit, "git", "", "canonical git URL to look up (required)")

	igManifestPushCmd.Flags().StringVar(&igManifestCatalog, "catalog", "", "target catalog (required)")
	igManifestPullCmd.Flags().StringVar(&igManifestOut, "out", "", "write the served manifest here (default: stdout)")

	igManifestCmd.AddCommand(igManifestPushCmd)
	igManifestCmd.AddCommand(igManifestPullCmd)

	igCmd.AddCommand(igListCmd)
	igCmd.AddCommand(igSyncCmd)
	igCmd.AddCommand(igStatusCmd)
	igCmd.AddCommand(igPublishCmd)
	igCmd.AddCommand(igClaimsCmd)
	igCmd.AddCommand(igManifestCmd)
	rootCmd.AddCommand(igCmd)
}

var igCmd = &cobra.Command{
	Use:   "ig",
	Short: "Fetch and publish ig catalogs (the only client of the ig catalog server)",
	Long: `ig builds a portable "catalog" — a graph-of-graphs over a project's code
repos plus its Facets infra. praxis is the ONLY network client of the ig
catalog server: it downloads catalogs and writes them to the filesystem;
the ` + "`ig`" + ` binary reads that filesystem and never talks to a server.

  praxis ig list                          the org's catalogs
  praxis ig sync <catalog> | --all        download a catalog into $IG_HOME
  praxis ig status [<catalog>]            is my local copy up to date?
  praxis ig publish <dir> --catalog --member   upload one member's graph
  praxis ig claims --git <url>            which catalogs claim this repo
  praxis ig manifest push <file> --catalog <c>
  praxis ig manifest pull <catalog> [--out <file>]

Catalogs materialize under $IG_HOME (default ~/.ig) at projects/<catalog>/,
with a .sync.json handshake beside each so ig knows how to refresh.`,
}

// --- list --------------------------------------------------------------

var igListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the org's catalogs (name, version, built_at, member count)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(igJSON, false, out)
		active := activeOrAuthExitProfile(out, igProfile)

		cats, err := igcatalog.ListCatalogs(active.Profile.URL, active.Profile.Token)
		if err != nil {
			return reportHTTPErr(out, active.Name, err)
		}
		if cats == nil {
			cats = []igcatalog.Catalog{}
		}
		if asJSON {
			return render.JSON(out, cats)
		}
		printCatalogsPretty(out, cats)
		return nil
	},
}

// --- sync --------------------------------------------------------------

var igSyncCmd = &cobra.Command{
	Use:   "sync [<catalog>]",
	Short: "Download a catalog's bundle into $IG_HOME and write .sync.json",
	Long: `Materializes a catalog under $IG_HOME/projects/<catalog>/ and writes a
.sync.json handshake beside it. Uses If-None-Match with the local digest so
an unchanged catalog is a cheap 304 and no re-download. Extraction is atomic
(temp dir + rename), and a digest mismatch fails loudly without touching the
live tree.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(igJSON, false, out)
		if igSyncAll && len(args) > 0 {
			usageExit(out, "pass either <catalog> or --all, not both", "")
		}
		if !igSyncAll && len(args) != 1 {
			usageExit(out, "sync requires exactly one <catalog> (or --all)", "praxis ig sync <catalog>")
		}
		active := activeOrAuthExitProfile(out, igProfile)

		targets := args
		if igSyncAll {
			cats, err := igcatalog.ListCatalogs(active.Profile.URL, active.Profile.Token)
			if err != nil {
				return reportHTTPErr(out, active.Name, err)
			}
			for _, c := range cats {
				targets = append(targets, c.Name)
			}
		}

		results := make([]map[string]string, 0, len(targets))
		for _, c := range targets {
			upToDate, err := syncOne(active, c)
			if err != nil {
				if errors.Is(err, errBundleMismatch) {
					render.PrintError(out, asJSON, fmt.Sprintf("sync %q: %v", c, err),
						"refusing to replace the local catalog on a digest mismatch", exitcode.Error)
					os.Exit(exitcode.Error)
				}
				if errors.Is(err, errBundleContract) {
					render.PrintError(out, asJSON, fmt.Sprintf("sync %q: %v", c, err),
						"the server sent a malformed catalog bundle; the local catalog was left untouched", exitcode.Error)
					os.Exit(exitcode.Error)
				}
				return reportHTTPErr(out, active.Name, err)
			}
			status := "synced"
			if upToDate {
				status = "up to date"
			}
			results = append(results, map[string]string{"catalog": c, "status": status})
		}
		if asJSON {
			return render.JSON(out, results)
		}
		for _, r := range results {
			fmt.Fprintf(out, "%s: %s\n", r["catalog"], r["status"])
		}
		return nil
	},
}

// --- status ------------------------------------------------------------

var igStatusCmd = &cobra.Command{
	Use:   "status [<catalog>]",
	Short: "Compare the local catalog against the server WITHOUT downloading",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(igJSON, false, out)
		active := activeOrAuthExitProfile(out, igProfile)

		var targets []string
		if len(args) == 1 {
			targets = []string{args[0]}
		} else {
			home, err := igHome()
			if err != nil {
				return reportHTTPErr(out, active.Name, err)
			}
			targets = syncedCatalogs(home)
		}

		results := make([]map[string]string, 0, len(targets))
		for _, c := range targets {
			state, serverVersion, localDigest, err := statusOne(active, c)
			if err != nil {
				return reportHTTPErr(out, active.Name, err)
			}
			results = append(results, map[string]string{
				"catalog": c, "status": state,
				"server_version": serverVersion, "local_digest": localDigest,
			})
		}
		if asJSON {
			return render.JSON(out, results)
		}
		if len(results) == 0 {
			fmt.Fprintln(out, "(no catalogs synced)")
			return nil
		}
		for _, r := range results {
			if r["status"] == "stale" {
				fmt.Fprintf(out, "%s: stale (server has %s)\n", r["catalog"], r["server_version"])
			} else {
				fmt.Fprintf(out, "%s: %s\n", r["catalog"], r["status"])
			}
		}
		return nil
	},
}

// --- publish -----------------------------------------------------------

var igPublishCmd = &cobra.Command{
	Use:   "publish <member-out-dir> --catalog <c> --member <m>",
	Short: "Upload one member's graph.json (gzipped) to a catalog",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(igJSON, false, out)
		if igPublishCatalog == "" || igPublishMember == "" {
			usageExit(out, "both --catalog and --member are required", "")
		}
		dir := args[0]
		memberDir := filepath.Join(dir, "member", igPublishMember)
		graphPath := filepath.Join(memberDir, "graphify-out", "graph.json")
		raw, err := os.ReadFile(graphPath)
		if err != nil {
			usageExit(out, fmt.Sprintf("read %s: %v", graphPath, err),
				"expected <dir>/member/<m>/graphify-out/graph.json")
		}

		git, sha := igPublishGit, igPublishSha
		if meta, ok := readMemberMeta(memberDir); ok {
			if git == "" {
				git = meta.Git
			}
			if sha == "" {
				sha = meta.Sha
			}
		}
		if git == "" || sha == "" {
			usageExit(out, "member git URL and sha are required",
				"provide --git/--sha, or a member-meta.json with {git, sha}")
		}

		active := activeOrAuthExitProfile(out, igProfile)

		gz := gzipBytes(raw)
		if err := igcatalog.PublishMember(active.Profile.URL, active.Profile.Token,
			igPublishCatalog, igPublishMember, gz, git, sha); err != nil {
			return reportHTTPErr(out, active.Name, err)
		}
		result := map[string]any{
			"catalog": igPublishCatalog, "member": igPublishMember,
			"git": git, "sha": sha, "bytes": len(gz), "status": "published",
		}
		if asJSON {
			return render.JSON(out, result)
		}
		fmt.Fprintf(out, "published %s/%s (%d bytes, sha %s)\n", igPublishCatalog, igPublishMember, len(gz), sha)
		return nil
	},
}

func readMemberMeta(memberDir string) (memberMeta, bool) {
	data, err := os.ReadFile(filepath.Join(memberDir, "member-meta.json"))
	if err != nil {
		return memberMeta{}, false
	}
	var m memberMeta
	if json.Unmarshal(data, &m) != nil {
		return memberMeta{}, false
	}
	return m, true
}

func gzipBytes(data []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(data)
	_ = zw.Close()
	return buf.Bytes()
}

// --- claims ------------------------------------------------------------

var igClaimsCmd = &cobra.Command{
	Use:   "claims --git <canonical-url>",
	Short: "Print the catalogs claiming a repo, one name per line (for CI loops)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		if igClaimsGit == "" {
			usageExit(out, "--git is required", "praxis ig claims --git https://github.com/org/repo.git")
		}
		active := activeOrAuthExitProfile(out, igProfile)

		names, err := igcatalog.Claims(active.Profile.URL, active.Profile.Token, igClaimsGit)
		if err != nil {
			return reportHTTPErr(out, active.Name, err)
		}
		// Default is newline-delimited so `for c in $(praxis ig claims …)`
		// works when piped; --json gives an array. (This verb intentionally
		// does NOT auto-JSON on a pipe — CI loops want bare lines.)
		if igJSON {
			if names == nil {
				names = []string{}
			}
			return render.JSON(out, names)
		}
		for _, n := range names {
			fmt.Fprintln(out, n)
		}
		return nil
	},
}

// --- manifest ----------------------------------------------------------

var igManifestCmd = &cobra.Command{
	Use:   "manifest",
	Short: "Push or pull a catalog's manifest text",
}

var igManifestPushCmd = &cobra.Command{
	Use:   "push <file> --catalog <c>",
	Short: "Upload a manifest, stamping pushed_by, pushed_at, and git_sha",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(igJSON, false, out)
		if igManifestCatalog == "" {
			usageExit(out, "--catalog is required", "praxis ig manifest push <file> --catalog <c>")
		}
		file := args[0]
		content, err := os.ReadFile(file)
		if err != nil {
			usageExit(out, fmt.Sprintf("read %s: %v", file, err), "")
		}

		// git_sha is best-effort: a manifest may come from a non-git dir.
		gitSHA, _ := gitHeadSHA(filepath.Dir(file))

		active := activeOrAuthExitProfile(out, igProfile)

		m := igcatalog.Manifest{
			Content:  string(content),
			PushedBy: active.Profile.Username,
			PushedAt: nowFn().UTC().Format(time.RFC3339),
			GitSHA:   gitSHA,
		}
		if err := igcatalog.ManifestPush(active.Profile.URL, active.Profile.Token, igManifestCatalog, m); err != nil {
			return reportHTTPErr(out, active.Name, err)
		}
		result := map[string]string{
			"catalog": igManifestCatalog, "pushed_by": m.PushedBy,
			"pushed_at": m.PushedAt, "git_sha": m.GitSHA, "status": "pushed",
		}
		if asJSON {
			return render.JSON(out, result)
		}
		fmt.Fprintf(out, "pushed manifest for %s (git_sha %s)\n", igManifestCatalog, m.GitSHA)
		return nil
	},
}

var igManifestPullCmd = &cobra.Command{
	Use:   "pull <catalog> [--out <file>]",
	Short: "Fetch the served manifest (verbatim) to diff against a local copy",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		active := activeOrAuthExitProfile(out, igProfile)

		m, err := igcatalog.ManifestPull(active.Profile.URL, active.Profile.Token, args[0])
		if err != nil {
			return reportHTTPErr(out, active.Name, err)
		}
		if igManifestOut != "" {
			if err := os.WriteFile(igManifestOut, []byte(m.Content), 0o644); err != nil {
				render.PrintError(out, true, fmt.Sprintf("write %s: %v", igManifestOut, err), "", exitcode.Error)
				os.Exit(exitcode.Error)
			}
			fmt.Fprintf(out, "wrote %s (%d bytes)\n", igManifestOut, len(m.Content))
			return nil
		}
		_, _ = out.Write([]byte(m.Content))
		return nil
	},
}

// --- pretty printers ---------------------------------------------------

func printCatalogsPretty(out io.Writer, cats []igcatalog.Catalog) {
	if len(cats) == 0 {
		fmt.Fprintln(out, "(no catalogs)")
		return
	}
	for _, c := range cats {
		fmt.Fprintf(out, "%s  %s  %s  (%d members)\n", c.Name, c.Version, c.BuiltAt, len(c.Members))
	}
}
