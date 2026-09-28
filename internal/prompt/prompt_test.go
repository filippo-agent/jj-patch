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

	"github.com/filippo-agent/jj-patch-interactive/internal/edit"
)

func sample() *edit.Session {
	return &edit.Session{Files: []edit.File{
		{Path: "one.txt", Kind: "modified", Hunks: []edit.Hunk{
			{Text: "alpha first\n"}, {Text: "beta second\n"}, {Text: "alpha third\n"},
		}},
		{Path: "two.txt", Kind: "modified", Hunks: []edit.Hunk{
			{Text: "beta fourth\n"}, {Text: "alpha fifth\n"},
		}},
	}}
}

func choices(s *edit.Session) string {
	var result strings.Builder
	for _, file := range s.Files {
		for _, hunk := range file.Hunks {
			if hunk.Selected {
				result.WriteByte('y')
			} else {
				result.WriteByte('n')
			}
		}
	}
	return result.String()
}

func TestCommands(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{"quit skips all", "q\n", "nnnnn"},
		{"quit saves earlier", "y\nq\n", "ynnnn"},
		{"quit preserves revisited selected", "y\nK\nq\n", "ynnnn"},
		{"revisit selected", "y\nK\nn\nq\n", "nnnnn"},
		{"revisit rejected", "n\nK\ny\nq\n", "ynnnn"},
		{"lower k skips decided", "y\nk\nn\nq\n", "ynnnn"},
		{"j and k undecided", "n\nj\nk\ny\nq\n", "nynnn"},
		{"upper J includes decided", "y\ny\ng 1\nJ\nn\nq\n", "ynnnn"},
		{"number inline", "g 4\ny\nq\n", "nnnyn"},
		{"number prompted", "g\n4\ny\nq\n", "nnnyn"},
		{"number compact", "g4\ny\nq\n", "nnnyn"},
		{"search inline", "/fourth\ny\nq\n", "nnnyn"},
		{"search prompted", "/\nfourth\ny\nq\n", "nnnyn"},
		{"search wraps", "g5\n/first\ny\nq\n", "ynnnn"},
		{"search revisits decided", "y\n/first\nn\nq\n", "nnnnn"},
		{"all", "A\n", "yyyyy"},
		{"all preserves rejection", "n\nA\n", "nyyyy"},
		{"file accept", "a\nq\n", "yyynn"},
		{"file reject", "d\nA\n", "nnnyy"},
		{"file accept later", "g4\na\nq\n", "nnnyy"},
		{"file accept skips earlier", "j\na\nq\n", "nyynn"},
		{"advance revisits skipped", "J\ny\ny\ny\ny\ny\n", "yyyyy"},
		{"normal rejection completion", "n\nn\nn\nn\nn\n", "nnnnn"},
		{"unknown uppercase stays unknown", "Y\nN\nD\nq\n", "nnnnn"},
		{"only exact commands", "yes\nno\nquit\nq\n", "nnnnn"},
		{"filter global accept", "G alpha\nA\nq\n", "ynyny"},
		{"filter accept finishes", "G alpha\nA\n", "ynyny"},
		{"filter clear retains decision", "G alpha\ny\nG\n\nq\n", "ynnnn"},
		{"filter accept clear preserves rejection", "n\nG alpha\ny\ny\nG\n\nA\n", "nyyyy"},
		{"invalid regex retains filter", "G alpha\nG [\nA\nq\n", "ynyny"},
		{"filter by path", "G two\\.txt\nA\nq\n", "nnnyy"},
		{"zero matches recover", "G nonexistent\nG\n\nA\n", "yyyyy"},
		{"zero matches quit", "G nonexistent\nq\n", "nnnnn"},
		{"filter does not accept hidden", "G alpha\na\nq\n", "ynynn"},
		{"filter search cannot escape", "G alpha\n/fourth\ny\nq\n", "ynnnn"},
		{"invalid numbers harmless", "g0\ng-1\ng999\ngwat\nq\n", "nnnnn"},
		{"invalid search harmless", "/[\nq\n", "nnnnn"},
		{"boundaries harmless", "k\nK\ng5\nj\nJ\nq\n", "nnnnn"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := sample()
			var out bytes.Buffer
			if err := Run(s, strings.NewReader(tt.input), &out, "in the first change"); err != nil {
				t.Fatalf("Run: %v\n%s", err, out.String())
			}
			if got := choices(s); got != tt.want {
				t.Fatalf("choices = %s, want %s\n%s", got, tt.want, out.String())
			}
			if !strings.Contains(out.String(), "Include this hunk in the first change?") {
				t.Fatalf("missing contextual prompt: %s", out.String())
			}
		})
	}
}

func TestAbortLeavesSessionUnchanged(t *testing.T) {
	for _, input := range []string{
		"", "y\n", "y", "q", "y\nQ\n", "Q\n", "g\n", "/\n", "G\n",
		"y\nG\n", "G nonexistent\n", "G alpha\ny\ny\ny\n", "?\n", "\n",
	} {
		t.Run(strings.ReplaceAll(input, "\n", "_"), func(t *testing.T) {
			s := sample()
			before := clone(s)
			err := Run(s, strings.NewReader(input), io.Discard, "")
			if !errors.Is(err, ErrAbort) {
				t.Fatalf("error = %v, want ErrAbort", err)
			}
			if !reflect.DeepEqual(*s, before) {
				t.Fatal("aborted selection mutated session")
			}
		})
	}
}

func TestLongCommand(t *testing.T) {
	input := "G " + strings.Repeat("a", 128*1024) + "\nG\n\nA\n"
	s := sample()
	if err := Run(s, strings.NewReader(input), io.Discard, ""); err != nil {
		t.Fatal(err)
	}
	if choices(s) != "yyyyy" {
		t.Fatal(choices(s))
	}
}

func TestCommandsDoNotConsumeFutureInput(t *testing.T) {
	const future = "n\nA\nfuture description editor input\n"
	for _, tc := range []struct {
		command string
		abort   bool
	}{
		{"A\n", false},
		{"q\n", false},
		{"y\nq\n", false},
		{"G alpha\nA\n", false},
		{"n\nn\nn\nn\nn\n", false},
		{"Q\n", true},
	} {
		t.Run(strings.ReplaceAll(tc.command, "\n", "_"), func(t *testing.T) {
			input := strings.NewReader(tc.command + future)
			err := Run(sample(), input, io.Discard, "")
			if (tc.abort && !errors.Is(err, ErrAbort)) || (!tc.abort && err != nil) {
				t.Fatalf("Run: %v", err)
			}
			leftover, err := io.ReadAll(input)
			if err != nil {
				t.Fatal(err)
			}
			if string(leftover) != future {
				t.Fatalf("consumed future editor input: leftover = %q, want %q", leftover, future)
			}
		})
	}
}

func TestMultipleInvocationsShareInput(t *testing.T) {
	input := strings.NewReader("y\nq\nn\nA\nfuture description\n")
	for i, want := range []string{"ynnnn", "nyyyy"} {
		s := sample()
		if err := Run(s, input, io.Discard, "to move to the destination"); err != nil {
			t.Fatalf("invocation %d: %v", i+1, err)
		}
		if got := choices(s); got != want {
			t.Fatalf("invocation %d choices = %s, want %s", i+1, got, want)
		}
	}
	leftover, err := io.ReadAll(input)
	if err != nil {
		t.Fatal(err)
	}
	if string(leftover) != "future description\n" {
		t.Fatalf("description editor input consumed: %q", leftover)
	}
}

func TestSafeOutput(t *testing.T) {
	s := sample()
	s.Files[0].Path = "bad\x1b]0;title\x07\nname\xff\u202e"
	s.Files[0].Kind = "kind\x1b[2J"
	s.Files[0].Hunks[0].Text = "line\x1b[2J\r\x00\t\x7f\u009b\u202e\xff\n"
	var out bytes.Buffer
	err := Run(s, strings.NewReader("bogus\x1b[2J\nG \x1b\nG\n\nq\n"), &out, "context\x1b[2J")
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, bad := range []string{"\x1b", "\x07", "\r", "\x00", "\t", "\x7f", "\u009b", "\u202e", "\xff"} {
		if strings.Contains(got, bad) {
			t.Fatalf("unsafe raw character %q in %q", bad, got)
		}
	}
	for _, escaped := range []string{`\x1b`, `\r`, `\t`, `\u202e`, `\xff`} {
		if !strings.Contains(got, escaped) {
			t.Errorf("missing printable escape %s", escaped)
		}
	}
}

func TestInitiallySelected(t *testing.T) {
	s := sample()
	s.Files[0].Hunks[0].Selected = true
	if err := Run(s, strings.NewReader("q\n"), io.Discard, ""); err != nil {
		t.Fatal(err)
	}
	if choices(s) != "ynnnn" {
		t.Fatal(choices(s))
	}
}

func TestEmptyAndNil(t *testing.T) {
	if err := Run(&edit.Session{}, strings.NewReader(""), io.Discard, ""); err != nil {
		t.Fatal(err)
	}
	if err := Run(nil, strings.NewReader(""), io.Discard, ""); err == nil {
		t.Fatal("nil session unexpectedly accepted")
	}
}

type failWriter struct{ err error }

func (w failWriter) Write([]byte) (int, error) { return 0, w.err }

type failReader struct{ err error }

func (r failReader) Read([]byte) (int, error) { return 0, r.err }

func TestIOErrors(t *testing.T) {
	boom := errors.New("broken transport")
	for _, tc := range []struct {
		name string
		in   io.Reader
		out  io.Writer
	}{
		{"writer", strings.NewReader("A\n"), failWriter{boom}},
		{"reader", failReader{boom}, io.Discard},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := sample()
			before := clone(s)
			err := Run(s, tc.in, tc.out, "")
			if !errors.Is(err, boom) {
				t.Fatalf("error = %v", err)
			}
			if !reflect.DeepEqual(*s, before) {
				t.Fatal("I/O error mutated session")
			}
		})
	}
}

func TestStripCommentsKeepsBlankContext(t *testing.T) {
	text := "# instructions\n@@ -1,3 +1,3 @@\n \n-old\n+new\n \n\n# trailer\n"
	want := "@@ -1,3 +1,3 @@\n \n-old\n+new\n \n\n"
	if got := stripComments(text); got != want {
		t.Fatalf("stripComments = %q, want %q", got, want)
	}
}

// realSession exercises the UI against actual engine splitting and validation.
func realSession(t *testing.T, old, new string) (*edit.Session, string) {
	t.Helper()
	root := t.TempDir()
	left, right := filepath.Join(root, "left"), filepath.Join(root, "right")
	for _, dir := range []string{left, right} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for path, data := range map[string]string{filepath.Join(left, "file"): old, filepath.Join(right, "file"): new} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := edit.Open(left, right, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, right
}

func TestSplitPreservesDecisions(t *testing.T) {
	old := "a\nold one\nmiddle\nold two\nz\n" + strings.Repeat("gap\n", 12) + "old tail\n"
	new := "a\nnew one\nmiddle\nnew two\nz\n" + strings.Repeat("gap\n", 12) + "new tail\n"
	for _, command := range []string{"s", "S"} {
		for _, decision := range []string{"y", "n"} {
			t.Run(command+decision, func(t *testing.T) {
				s, _ := realSession(t, old, new)
				// The far-away tail keeps selection open after deciding the
				// first (splittable) hunk.
				if len(s.Files[0].Hunks) != 2 {
					t.Fatal("fixture must initially contain two hunks")
				}
				input := decision + "\nK\n" + command + "\nq\n"
				if err := Run(s, strings.NewReader(input), io.Discard, ""); err != nil {
					t.Fatal(err)
				}
				want := decision + decision + "n"
				if choices(s) != want {
					t.Fatalf("choices = %s, want %s", choices(s), want)
				}
			})
		}
	}
}

func TestSplitThenSelect(t *testing.T) {
	s, right := realSession(t, "a\nold one\nmiddle\nold two\nz\n", "a\nnew one\nmiddle\nnew two\nz\n")
	original, _ := os.ReadFile(filepath.Join(right, "file"))
	var out bytes.Buffer
	if err := Run(s, strings.NewReader("s\ny\nn\n"), &out, ""); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if choices(s) != "yn" {
		t.Fatalf("choices: %s", choices(s))
	}
	after, _ := os.ReadFile(filepath.Join(right, "file"))
	if !bytes.Equal(original, after) {
		t.Fatal("prompt wrote to snapshot")
	}
	if err := s.Write(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(right, "file"))
	if string(got) != "a\nnew one\nmiddle\nold two\nz\n" {
		t.Fatalf("result = %q", got)
	}
}

func TestSplitAllKeepsCursorAndHonorsFilter(t *testing.T) {
	t.Run("cursor follows original hunk", func(t *testing.T) {
		old := "a\nold one\nmiddle\nold two\nz\n" + strings.Repeat("gap\n", 12) + "old tail\n"
		new := "a\nnew one\nmiddle\nnew two\nz\n" + strings.Repeat("gap\n", 12) + "new tail\n"
		s, _ := realSession(t, old, new)
		if err := Run(s, strings.NewReader("y\nS\nn\n"), io.Discard, ""); err != nil {
			t.Fatal(err)
		}
		if choices(s) != "yyn" {
			t.Fatalf("choices = %s", choices(s))
		}
	})
	t.Run("filter reevaluated after splitting", func(t *testing.T) {
		s, _ := realSession(t, "a\nold one\nmiddle\nold two\nz\n", "a\nnew one\nmiddle\nnew two\nz\n")
		if err := Run(s, strings.NewReader("G new one\nS\nA\n"), io.Discard, ""); err != nil {
			t.Fatal(err)
		}
		if choices(s) != "yn" {
			t.Fatalf("choices = %s", choices(s))
		}
	})
}
