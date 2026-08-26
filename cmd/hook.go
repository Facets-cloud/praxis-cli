package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// `praxis hook user-prompt-submit` — wired into every hook-capable AI host by
// `praxis login`. When the prompt mentions a Facets term it asks the agent to
// look for a relevant praxis-* skill; silent + exit 0 otherwise, since a hook
// must never block a prompt.

// triggerWords are Facets terms worth a second look. Kept generic on purpose:
// the agent picks the skill, so this only has to be right about "is this a
// Facets question at all".
var triggerWords = []string{
	"environment",
	"blueprint",
	"facets",
	"facets.yaml",
	"raptor",
	"praxis",
	"override",
	"overrides",
	"control plane",
}

const nudge = "This prompt mentions Facets. Check whether a skill named praxis-* is " +
	"relevant and invoke it before doing any other work. If none fit, say so in one line and carry on."

// promptEventName is echoed back when the host omits its own. Claude Code and
// Codex say UserPromptSubmit, Gemini says BeforeAgent.
const promptEventName = "UserPromptSubmit"

// matches reports whether prompt mentions a trigger word. Path-like tokens are
// dropped first: "facets" and "praxis" appear in every checkout under
// facets-repos, so "cd into ~/facets-repos/raptor" is not a Facets question.
func matches(prompt string) bool {
	var words []string
	for _, f := range strings.Fields(prompt) {
		if !strings.ContainsAny(f, `/\`) {
			words = append(words, f)
		}
	}
	// Padded so a term at either end still has a boundary byte to test.
	hay := strings.ToLower(" " + strings.Join(words, " ") + " ")
	for _, w := range triggerWords {
		if containsBounded(hay, w) {
			return true
		}
	}
	return false
}

// containsBounded reports whether term appears in hay delimited by
// non-alphanumerics, so "override" does not fire on "overridden". hay must be
// lowercased and space-padded.
func containsBounded(hay, term string) bool {
	for i := 0; ; {
		j := strings.Index(hay[i:], term)
		if j < 0 {
			return false
		}
		s := i + j
		if !isWordByte(hay[s-1]) && !isWordByte(hay[s+len(term)]) {
			return true
		}
		i = s + 1
	}
}

func isWordByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z')
}

var hookCmd = &cobra.Command{
	Use:    "hook <user-prompt-submit>",
	Short:  "AI-host hook: nudge toward the praxis skills",
	Hidden: true, // wired by `praxis login`, not called by hand
	Args:   cobra.ExactArgs(1),
	// A hook's stderr must stay quiet; a bad arg is a wiring bug.
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if args[0] != "user-prompt-submit" {
			return fmt.Errorf("unknown hook %q (want user-prompt-submit)", args[0])
		}
		var p struct {
			Prompt        string `json:"prompt"`
			HookEventName string `json:"hook_event_name"`
		}
		if b, rErr := io.ReadAll(cmd.InOrStdin()); rErr == nil && len(b) > 0 {
			_ = json.Unmarshal(b, &p)
		}
		if !matches(p.Prompt) {
			return nil
		}
		event := p.HookEventName
		if event == "" {
			event = promptEventName
		}
		out, err := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{
			"hookEventName":     event,
			"additionalContext": nudge,
		}})
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(out))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(hookCmd)
}
