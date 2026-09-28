package prompt

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func editorScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test editor.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+body+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return "sh " + shellQuote(path) + " 'argument with spaces'"
}

// Match production's os.Stdin: exec.Cmd passes an *os.File directly to the
// child. A strings.Reader would instead start an os/exec copying goroutine,
// which itself consumes future prompt commands even if the editor never reads.
func editorInput(t *testing.T, transcript string) *os.File {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close() })
	if _, err := io.WriteString(writer, transcript); err != nil {
		writer.Close()
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return reader
}

func TestEditorCommandAndQuotedTemporaryPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "unsafe ' ; $(touch SHOULD_NOT_EXIST) ; space")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", dir)
	t.Setenv("EDITOR", "exit 97")
	t.Setenv("VISUAL", editorScript(t, `
test "$1" = "argument with spaces"
test -f "$2"
sed 's/^+new$/+edited/' "$2" > "$2.next"
mv "$2.next" "$2"
`))
	s, right := realSession(t, "old\n", "new\n")
	if err := Run(s, editorInput(t, "e\n"), io.Discard, ""); err != nil {
		t.Fatal(err)
	}
	if !s.Files[0].Hunks[0].Selected || !strings.Contains(s.Files[0].Hunks[0].Text, "+edited\n") {
		t.Fatalf("editor did not select edited hunk: %+v", s.Files)
	}
	if err := s.Write(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(right, "file"))
	if string(got) != "edited\n" {
		t.Fatalf("result = %q", got)
	}
	matches, err := filepath.Glob(filepath.Join(dir, "jj-patch-interactive-hunk-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary edit files not removed: %v, %v", matches, err)
	}
}

func TestEditorFallbackAndBlankContext(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", editorScript(t, `test -f "$2"`))
	s, right := realSession(t, "\nold\n\n", "\nnew\n\n")
	want := s.Files[0].Hunks[0].Text
	if !strings.Contains(want, "\n \n") {
		t.Fatalf("fixture has no blank context: %q", want)
	}
	if err := Run(s, editorInput(t, "e\n"), io.Discard, ""); err != nil {
		t.Fatal(err)
	}
	if s.Files[0].Hunks[0].Text != want {
		t.Fatalf("blank context lost: %q", s.Files[0].Hunks[0].Text)
	}
	if err := s.Write(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(right, "file"))
	if string(got) != "\nnew\n\n" {
		t.Fatalf("result = %q", got)
	}
}

func TestEditorRetryRetainsFailedText(t *testing.T) {
	for _, failure := range []string{"validation", "editor exit"} {
		t.Run(failure, func(t *testing.T) {
			firstExit := "0"
			if failure == "editor exit" {
				firstExit = "42"
			}
			t.Setenv("VISUAL", editorScript(t, `
if ! grep -q '^broken first edit$' "$2"; then
  printf 'broken first edit\n' > "$2"
  exit `+firstExit+`
fi
printf '@@ -1 +1 @@\n-old\n+fixed\n' > "$2"
`))
			s, _ := realSession(t, "old\n", "new\n")
			var out bytes.Buffer
			if err := Run(s, editorInput(t, "e\ny\n"), &out, ""); err != nil {
				t.Fatalf("%v\n%s", err, out.String())
			}
			if !strings.Contains(s.Files[0].Hunks[0].Text, "+fixed\n") || choices(s) != "y" {
				t.Fatalf("retry failed: %+v", s.Files)
			}
			if !strings.Contains(out.String(), "Edit again") {
				t.Fatal("failed edit did not prompt for retry")
			}
		})
	}
}

func TestEditorCancelAndEOF(t *testing.T) {
	t.Setenv("VISUAL", editorScript(t, `printf 'broken edit\n' > "$2"`))
	for _, input := range []string{"e\nn\nq\n", "e\n", "e\nQ\n", "e\nwhat\nn\nq\n"} {
		t.Run(strings.ReplaceAll(input, "\n", "_"), func(t *testing.T) {
			s, right := realSession(t, "old\n", "new\n")
			before := clone(s)
			err := Run(s, editorInput(t, input), io.Discard, "")
			if strings.HasSuffix(input, "q\n") {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ErrAbort) {
				t.Fatalf("error = %v, want ErrAbort", err)
			}
			if !reflect.DeepEqual(s.Files, before.Files) {
				t.Fatal("unsuccessful edit changed session")
			}
			got, _ := os.ReadFile(filepath.Join(right, "file"))
			if string(got) != "new\n" {
				t.Fatalf("prompt changed snapshot: %q", got)
			}
		})
	}
}

func TestAbortAfterSplitDoesNotWrite(t *testing.T) {
	for _, input := range []string{"s\ny\nQ\n", "s\ny\n"} {
		s, right := realSession(t, "a\nold one\nmiddle\nold two\nz\n", "a\nnew one\nmiddle\nnew two\nz\n")
		before := clone(s)
		err := Run(s, strings.NewReader(input), io.Discard, "")
		if !errors.Is(err, ErrAbort) {
			t.Fatalf("error = %v", err)
		}
		if !reflect.DeepEqual(s.Files, before.Files) {
			t.Fatal("aborted split changed public session")
		}
		got, _ := os.ReadFile(filepath.Join(right, "file"))
		if string(got) != "a\nnew one\nmiddle\nnew two\nz\n" {
			t.Fatalf("abort changed snapshot: %q", got)
		}
		// Explicitly using the ORIGINAL session again must remain valid. This
		// detects private states/hunks accidentally shared with the UI copy.
		if err := s.Write(); err != nil {
			t.Fatalf("aborted split corrupted original engine state: %v", err)
		}
		got, _ = os.ReadFile(filepath.Join(right, "file"))
		if string(got) != "a\nold one\nmiddle\nold two\nz\n" {
			t.Fatalf("original session retained aborted split selections: %q", got)
		}
	}
}

func TestEmptyEditorBufferCancelsWithoutChangingDecision(t *testing.T) {
	for _, content := range []string{"", " \n\t\n", "# instructions only\n", "# instructions\n \n\n"} {
		for _, decision := range []string{"undecided", "included", "excluded"} {
			t.Run(decision+":"+content, func(t *testing.T) {
				t.Setenv("VISUAL", editorScript(t, `printf '%s' `+shellQuote(content)+` > "$2"`))
				s := sample()
				prefix := ""
				want := "nnnnn"
				finish := "q\n"
				if decision == "included" {
					prefix, want = "y\nK\n", "ynnnn"
				} else if decision == "excluded" {
					prefix = "n\nK\n"
				} else {
					// A must still consider the canceled hunk undecided,
					// rather than treating cancellation as a rejection.
					finish, want = "A\n", "yyyyy"
				}
				originalText := s.Files[0].Hunks[0].Text
				var out bytes.Buffer
				if err := Run(s, editorInput(t, prefix+"e\n"+finish), &out, ""); err != nil {
					t.Fatalf("%v\n%s", err, out.String())
				}
				if choices(s) != want || s.Files[0].Hunks[0].Text != originalText {
					t.Fatalf("empty edit changed hunk: %+v", s.Files[0].Hunks[0])
				}
				if strings.Contains(out.String(), "Edit again") || strings.Contains(out.String(), "not accepted") {
					t.Fatalf("empty edit prompted for retry: %s", out.String())
				}
			})
		}
	}
}

func TestAbortAfterSuccessfulEditPreservesEngineState(t *testing.T) {
	t.Setenv("VISUAL", editorScript(t, `
sed 's/^+new one$/+edited one/' "$2" > "$2.next"
mv "$2.next" "$2"
`))
	old := "a\nold one\nz\n" + strings.Repeat("gap\n", 12) + "old tail\n"
	new := "a\nnew one\nz\n" + strings.Repeat("gap\n", 12) + "new tail\n"
	for _, input := range []string{"e\nQ\n", "e\n"} {
		t.Run(strings.ReplaceAll(input, "\n", "_"), func(t *testing.T) {
			s, right := realSession(t, old, new)
			before := clone(s)
			var out bytes.Buffer
			err := Run(s, editorInput(t, input), &out, "")
			if !errors.Is(err, ErrAbort) {
				t.Fatalf("error = %v\n%s", err, out.String())
			}
			if strings.Contains(out.String(), "not accepted") {
				t.Fatalf("fixture edit did not succeed: %s", out.String())
			}
			if !reflect.DeepEqual(s.Files, before.Files) {
				t.Fatal("aborted edit changed public session")
			}
			got, _ := os.ReadFile(filepath.Join(right, "file"))
			if string(got) != new {
				t.Fatalf("aborted edit wrote snapshot: %q", got)
			}
			if err := s.Write(); err != nil {
				t.Fatalf("aborted edit corrupted original engine state: %v", err)
			}
			got, _ = os.ReadFile(filepath.Join(right, "file"))
			if string(got) != old {
				t.Fatalf("original session retained aborted edit: %q", got)
			}
		})
	}
}

func TestEditorReceivesPastedInputWithoutConsumingFutureCommands(t *testing.T) {
	t.Setenv("VISUAL", editorScript(t, `
IFS= read -r answer
test "$answer" = "pasted editor input"
: > "$2"
`))
	input := editorInput(t, "e\npasted editor input\nq\nfuture description editor input\n")
	var out bytes.Buffer
	if err := Run(sample(), input, &out, ""); err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "not accepted") {
		t.Fatalf("external editor did not receive its input: %s", out.String())
	}
	leftover, err := io.ReadAll(input)
	if err != nil {
		t.Fatal(err)
	}
	if string(leftover) != "future description editor input\n" {
		t.Fatalf("future input consumed: %q", leftover)
	}
}
