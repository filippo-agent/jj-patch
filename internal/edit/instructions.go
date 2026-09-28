package edit

import "strings"

const instructionsName = "JJ-INSTRUCTIONS"

// IsInstructions recognizes jj's generated diff-editor instructions by known
// preambles, not merely the filename. Main/UI should use Session.Instructions,
// rather than reading JJ-INSTRUCTIONS independently (which might be a symlink or
// a real tracked file). The detector is intentionally conservative. The format
// is from jj cli/src/merge_tools/diff_working_copies.rs and commands/{diffedit,
// squash,split,restore,commit,absorb}.rs. Unknown future formats are ordinary files.
func IsInstructions(data []byte) bool {
	text := string(data)
	const explanation = "You are using the experimental 3-pane diff editor config. Some of\nthe following instructions may have been written with a 2-pane\ndiff editing in mind and be a little inaccurate.\n\n"
	for _, prefix := range []string{"Please make your edits in this pane.\n\n" + explanation, "The content of this pane should NOT be edited. Any edits will be\nlost.\n\n" + explanation} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	for _, pair := range [][2]string{
		{"You are editing changes in: ", "Adjust the right side until it shows the contents you want."},
		{"You are moving changes from: ", "Adjust the right side until the diff shows the changes you want to move"},
		{"You are splitting a commit into two: ", "The diff initially shows the changes in the commit you're splitting."},
		{"You are restoring changes from: ", "The diff initially shows all changes restored."},
		{"You are splitting the working-copy commit: ", "contents you want for the first commit. The remainder will be included in the"},
		{"You are selecting changes from: ", "absorption into ancestors."},
	} {
		if strings.HasPrefix(text, pair[0]) && strings.Contains(text, "\n\n") && strings.Contains(text, pair[1]) {
			if pair[0] == "You are selecting changes from: " && !strings.Contains(text, "absorb. Selected hunks will be considered for assignment to the") {
				return false
			}
			return true
		}
	}
	return false
}
