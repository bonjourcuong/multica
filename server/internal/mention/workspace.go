package mention

import "regexp"

// WorkspaceMention is one parsed `@workspace[:agent]` reference extracted from
// free-form text by ParseWorkspaceMentions.
type WorkspaceMention struct {
	WorkspaceSlug string
	// AgentName is optional. When empty the dispatch lands in the workspace
	// mirror session without targeting a specific resident agent.
	AgentName string
}

// workspaceMentionRe matches `@slug` or `@slug:Agent` where:
//   - slug starts with a lowercase letter or digit, contains lowercase letters,
//     digits and dashes, min 2 chars total. Strict to avoid catching `@` glyphs
//     inside email addresses or random text.
//   - the agent suffix is optional and matches a typical agent name shape
//     (letters/digits/underscores/dashes, must start with a letter).
//
// The leading `(?:^|\s)` makes sure we don't capture the local part of an
// email address (`foo@bar.com`).
var workspaceMentionRe = regexp.MustCompile(
	`(?:^|\s)@([a-z0-9][a-z0-9-]+)(?::([A-Za-z][A-Za-z0-9_-]*))?`,
)

// ParseWorkspaceMentions extracts every `@workspace[:agent]` reference from s.
//
// Order is preserved. Duplicates are NOT deduplicated — the dispatcher decides
// whether to ignore repeated targets. Email addresses (`foo@bar.com`) and
// inline references where `@` is preceded by non-whitespace are intentionally
// ignored.
func ParseWorkspaceMentions(s string) []WorkspaceMention {
	matches := workspaceMentionRe.FindAllStringSubmatch(s, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]WorkspaceMention, 0, len(matches))
	for _, m := range matches {
		out = append(out, WorkspaceMention{WorkspaceSlug: m[1], AgentName: m[2]})
	}
	return out
}
