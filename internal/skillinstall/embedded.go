package skillinstall

import (
	"embed"
	"io/fs"
)

// onboardingTreeFiles holds the binary-embedded source of the
// `praxis-onboarding` skill — a multi-file (tree) skill: a SKILL.md engine
// plus per-flow files under flows/. Unlike the single-file meta-skills in
// dummy.go (string bodies) and unlike org catalog skills (fetched from the
// server), this content ships inside the binary as a real file tree so it
// can carry multiple files.
//
//go:embed embedded/praxis-onboarding
var onboardingTreeFiles embed.FS

// treeSkills maps a binary-embedded multi-file skill name to its rooted file
// tree (SKILL.md at the root, plus subdirectories like flows/). Tree skills
// install via InstallTree instead of the single-file ContentFor path, and
// they are meta-skills (preserved on profile switch — see IsMetaSkill).
func treeSkills() map[string]fs.FS {
	// The embed path is a compile-time constant, so fs.Sub cannot fail in a
	// correctly-built binary.
	sub, err := fs.Sub(onboardingTreeFiles, "embedded/praxis-onboarding")
	if err != nil {
		panic("skillinstall: embedded praxis-onboarding tree missing: " + err.Error())
	}
	return map[string]fs.FS{
		"praxis-onboarding": sub,
	}
}

// isTreeSkill reports whether `name` is a binary-embedded multi-file skill.
func isTreeSkill(name string) bool {
	_, ok := treeSkills()[name]
	return ok
}

// treeSkillFS returns the embedded file tree for a tree skill, if any.
func treeSkillFS(name string) (fs.FS, bool) {
	fsys, ok := treeSkills()[name]
	return fsys, ok
}
