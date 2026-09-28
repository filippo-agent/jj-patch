package prompt

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func (p *prompt) editCurrent() (bool, error) {
	file, err := os.CreateTemp("", "jj-patch-interactive-hunk-*.diff")
	if err != nil {
		p.printf("Cannot create edit file: %s\n", printable(err.Error()))
		return false, nil
	}
	path := file.Name()
	defer os.Remove(path)
	text := p.session.Files[p.current.file].Hunks[p.current.hunk].Text
	instructions := "# Edit this patch. Delete unwanted '+' lines; change unwanted '-' to context (' ').\n" +
		"# Lines beginning with '#' are removed; blank and context lines are preserved.\n" +
		"# An empty file cancels this edit without changing your selection.\n"
	for _, line := range strings.Split(p.session.Instructions, "\n") {
		if line != "" {
			instructions += "# " + line + "\n"
		}
	}
	if _, err = file.WriteString(instructions + text); err != nil {
		file.Close()
		return false, fmt.Errorf("write edit file: %w", err)
	}
	if err = file.Close(); err != nil {
		return false, fmt.Errorf("close edit file: %w", err)
	}

	editor := os.Getenv("VISUAL")
	if strings.TrimSpace(editor) == "" {
		editor = os.Getenv("EDITOR")
	}
	if strings.TrimSpace(editor) == "" {
		editor = "vi"
	}
	for {
		// The editor is a user-supplied shell command (possibly with arguments).
		// The temporary path is never interpolated into shell source.
		cmd := exec.Command("sh", "-c", editor+` "$1"`, "jj-patch-interactive-editor", path)
		cmd.Stdin = p.input
		cmd.Stdout = p.out
		cmd.Stderr = p.out
		err := cmd.Run()
		if p.out.err != nil {
			return false, p.out.err
		}
		if err == nil {
			var data []byte
			data, err = os.ReadFile(path)
			if err == nil {
				edited := stripComments(string(data))
				// An empty (or comments/whitespace-only) buffer cancels the
				// edit. Trim only for this check, never individual diff lines:
				// a line containing one space is meaningful blank context.
				if strings.TrimSpace(edited) == "" {
					return false, nil
				}
				// Validate on a copy: a rejected edit cannot partially change
				// the working session even if the engine mutates before error.
				candidate := clone(p.session)
				err = candidate.Edit(p.current.file, p.current.hunk, edited)
				if err == nil {
					*p.session = candidate
					return true, nil
				}
			}
		}
		p.printf("Edited hunk was not accepted: %s\n", printable(err.Error()))
		for {
			p.printf("Edit again (n discards this edit) [y/n/Q]? ")
			answer, readErr := p.readLine()
			if readErr != nil {
				return false, readErr
			}
			switch answer {
			case "y":
				// Reopen the SAME file, preserving the unsuccessful edit.
				goto retry
			case "n":
				return false, nil
			case "Q":
				return false, ErrAbort
			default:
				p.printf("Please answer y, n, or Q.\n")
			}
		}
	retry:
	}
}

func stripComments(text string) string {
	var result strings.Builder
	for _, line := range strings.SplitAfter(text, "\n") {
		if !strings.HasPrefix(line, "#") {
			result.WriteString(line)
		}
	}
	return result.String()
}
