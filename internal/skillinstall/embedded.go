package skillinstall

import (
	"embed"
	"io/fs"
)

// treeSkillFiles holds the binary-embedded source of every multi-file (tree)
// skill — each a SKILL.md engine plus optional subdirectories (e.g.
// praxis-onboarding's flows/). Unlike the single-file meta-skills in dummy.go
// (string bodies) and unlike org catalog skills (fetched from the server),
// this content ships inside the binary as a real file tree so it can carry
// multiple files.
//
//   - praxis-onboarding: the guided getting-started journey.
//   - use-ig: the Praxis-MCP read variant of ig's use-ig skill; reads run
//     server-side via `praxis mcp ig`, so the host needs no local `ig`.
//
//go:embed embedded/praxis-onboarding embedded/use-ig
var treeSkillFiles embed.FS

// treeSkillNames is the set of binary-embedded tree skills, in the order they
// are declared in the embed directive above.
var treeSkillNames = []string{"praxis-onboarding", "use-ig"}

// treeSkills maps a binary-embedded multi-file skill name to its rooted file
// tree (SKILL.md at the root, plus subdirectories like flows/). Tree skills
// install via InstallTree instead of the single-file ContentFor path, and
// they are meta-skills (preserved on profile switch — see IsMetaSkill).
func treeSkills() map[string]fs.FS {
	out := make(map[string]fs.FS, len(treeSkillNames))
	for _, name := range treeSkillNames {
		// The embed paths are compile-time constants, so fs.Sub cannot fail
		// in a correctly-built binary.
		sub, err := fs.Sub(treeSkillFiles, "embedded/"+name)
		if err != nil {
			panic("skillinstall: embedded tree skill missing: " + name + ": " + err.Error())
		}
		out[name] = sub
	}
	return out
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
