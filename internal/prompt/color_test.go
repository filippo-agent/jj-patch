package prompt

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestColorDiffSanitizesBeforeStyling(t *testing.T) {
	const text = "@@ -1,2 +1,2 @@\n-\told\x1b[2J\n+\tnew\x1b[31m\n \tcontext\n"
	var colored, plain bytes.Buffer
	(&prompt{out: &output{Writer: &colored}, color: true}).showDiff(text)
	(&prompt{out: &output{Writer: &plain}}).showDiff(text)
	for _, code := range []string{"36", "32", "31"} {
		if !strings.Contains(colored.String(), "\x1b["+code+"m") {
			t.Errorf("missing ANSI style %s: %q", code, colored.String())
		}
	}
	if strings.Contains(colored.String(), "\x1b[2J") {
		t.Fatal("untrusted terminal control sequence emitted")
	}
	// Strip precisely the styles produced by the renderer; patch-provided ANSI
	// remains printable escaped text, not an executable terminal sequence.
	unstyle := rendererSGR
	if got := unstyle.ReplaceAllString(colored.String(), ""); got != plain.String() {
		t.Fatalf("styled output differs from safe plain output: %q != %q", got, plain.String())
	}
	if plain.String() != printableDiff(text) {
		t.Fatalf("plain rendering changed patch: %q", plain.String())
	}
	for _, line := range []string{"-\told", "+\tnew", " \tcontext"} {
		if !strings.Contains(plain.String(), line) {
			t.Fatalf("diff tab was escaped: %q", plain.String())
		}
	}
}

func TestDiffTabsAreDistinctFromLiteralEscapes(t *testing.T) {
	text := "+\t\t<key>name</key>\t<!-- comment -->\n+literal\\ttext\n"
	if got := printableDiff(text); got != text {
		t.Fatalf("tabs or literal backslashes changed: %q", got)
	}
	if got := printable("metadata\tvalue"); got != `metadata\tvalue` {
		t.Fatalf("metadata tab must remain escaped: %q", got)
	}
}

func TestColorDisabledForNonTerminals(t *testing.T) {
	t.Setenv("TERM", "xterm")
	t.Setenv("NO_COLOR", "")
	os.Unsetenv("NO_COLOR") // t.Setenv still restores the original environment.
	var buffer bytes.Buffer
	if colorEnabled(&buffer) || colorEnabled(io.Discard) {
		t.Fatal("color enabled for ordinary writer")
	}
	file, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if colorEnabled(file) {
		t.Fatal("color enabled for redirected file")
	}
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	if colorEnabled(null) {
		t.Fatal("character devices are not necessarily terminals")
	}
}

func TestContextPhrases(t *testing.T) {
	for _, context := range []string{
		"in the first change", "in the edited change",
		"to move to the destination", "to restore",
	} {
		var out bytes.Buffer
		if err := Run(sample(), strings.NewReader("q\n"), &out, context); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "Include this hunk "+context+"?") {
			t.Fatalf("unnatural prompt for %q: %s", context, out.String())
		}
	}
}
