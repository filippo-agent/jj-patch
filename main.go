// jj-patch-interactive is a directory diff editor for Jujutsu.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/filippo-agent/jj-patch-interactive/internal/edit"
	"github.com/filippo-agent/jj-patch-interactive/internal/prompt"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "jj-patch-interactive:", err)
		os.Exit(1)
	}
}

func run(args []string, in io.Reader, out, errout io.Writer) error {
	fs := flag.NewFlagSet("jj-patch-interactive", flag.ContinueOnError)
	fs.SetOutput(errout)
	output := fs.String("output", "", "write a three-directory edit to this directory instead of RIGHT")
	noInstructions := fs.Bool("no-instructions", false, "treat JJ-INSTRUCTIONS as an ordinary file (use with jj's ui.diff-instructions=false)")
	context := fs.String("context", "auto", "prompt context: auto, split, commit, diffedit, squash, restore, absorb, generic")
	version := fs.Bool("version", false, "print version")
	fs.Usage = func() {
		fmt.Fprintln(errout, "Usage: jj-patch-interactive [--output OUTPUT] [--context CONTEXT] LEFT RIGHT\n\nA git-add-p-style diff editor for jj. Configure ui.diff-editor = \"jj-patch-interactive\".\nSelect changes with y/n; q saves selections; Q or EOF aborts without saving.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *version {
		v := "devel"
		if info, ok := debug.ReadBuildInfo(); ok {
			if info.Main.Version != "" && info.Main.Version != "(devel)" {
				v = info.Main.Version
			}
			for _, setting := range info.Settings {
				if setting.Key == "vcs.revision" {
					v += " " + setting.Value
				}
			}
		}
		fmt.Fprintln(out, "jj-patch-interactive", v)
		return nil
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return errors.New("expected LEFT and RIGHT directories")
	}
	if _, err := promptContext(*context, ""); err != nil {
		return err
	}
	s, err := edit.OpenWithOptions(fs.Arg(0), fs.Arg(1), *output, edit.Options{Instructions: !*noInstructions})
	if err != nil {
		return err
	}
	defer s.Close()
	phrase, err := promptContext(*context, s.Instructions)
	if err != nil {
		return err
	}
	if err := prompt.Run(s, in, out, phrase); err != nil {
		return err
	}
	return s.Write()
}

// JJ-INSTRUCTIONS is human-readable, not a stable command identifier. Unknown
// instructions deliberately get a generic prompt rather than a guessed action.
func promptContext(context, instructions string) (string, error) {
	if context == "auto" {
		// Strip only jj's known three-pane wrapper. Commit descriptions can
		// contain arbitrary text, including other command preambles.
		if strings.HasPrefix(instructions, "Please make your edits in this pane.\n\n") ||
			strings.HasPrefix(instructions, "The content of this pane should NOT be edited.") {
			const end = "diff editing in mind and be a little inaccurate.\n\n"
			if _, body, ok := strings.Cut(instructions, end); ok {
				instructions = body
			}
		}
		switch {
		case strings.HasPrefix(instructions, "You are splitting the working-copy commit:"):
			context = "commit"
		case strings.HasPrefix(instructions, "You are splitting a commit into two:"):
			context = "split"
		case strings.HasPrefix(instructions, "You are editing changes in:"):
			context = "diffedit"
		case strings.HasPrefix(instructions, "You are moving changes from:"):
			context = "squash"
		case strings.HasPrefix(instructions, "You are restoring changes from:"):
			context = "restore"
		case strings.HasPrefix(instructions, "You are selecting changes from:") &&
			strings.Contains(instructions, "absorption into ancestors."):
			context = "absorb"
		default:
			context = "generic"
		}
	}
	switch context {
	case "split", "commit":
		return "in the first change", nil
	case "diffedit":
		return "in the edited change", nil
	case "squash":
		return "to move to the destination", nil
	case "restore":
		return "to restore", nil
	case "absorb":
		return "for absorption into ancestors", nil
	case "generic":
		return "in the result", nil
	default:
		return "", fmt.Errorf("unknown context %q", context)
	}
}
