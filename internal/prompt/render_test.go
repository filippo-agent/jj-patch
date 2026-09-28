package prompt

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

var rendererSGR = regexp.MustCompile("\x1b\\[(?:0|1|1;33|31|32|36|30;43|97;41)m")

func assertSafeRendered(t testing.TB, text string, color bool) string {
	t.Helper()
	if !color && strings.ContainsRune(text, '\x1b') {
		t.Fatalf("plain output contains ESC: %q", text)
	}
	plain := rendererSGR.ReplaceAllString(text, "")
	if !utf8.ValidString(plain) {
		t.Fatalf("invalid UTF-8 emitted: %q", plain)
	}
	for _, r := range plain {
		if r != '\n' && r != '\t' && isNonDisplayable(r) {
			t.Fatalf("unsafe raw character %U in output %q", r, plain)
		}
	}
	return plain
}

func TestRendererOwnsEveryControlSequence(t *testing.T) {
	var payload strings.Builder
	for r := rune(0); r <= 0x1F; r++ {
		payload.WriteRune(r)
	}
	for r := rune(0x7F); r <= 0x9F; r++ {
		payload.WriteRune(r)
	}
	for b := 0x80; b <= 0xFF; b++ {
		payload.WriteByte(byte(b))
	}
	payload.WriteString("\x1b[32m\x1b[0m\x1b[2J\x1b]52;c;Y2xpcGJvYXJk\a\x1b]8;;https://example.invalid\x1b\\link\x1b]8;;\x1b\\")
	payload.WriteString("\u009b31m\u009d52;c;YQ==\u009c\x1bPmalicious DCS\x1b\\")
	text := "+" + payload.String() + "\n"
	plain := renderDiff(text, false)
	assertSafeRendered(t, plain, false)
	colored := renderDiff(text, true)
	if got := assertSafeRendered(t, colored, true); got != plain {
		t.Fatalf("color changed display: %q != %q", got, plain)
	}
	for _, escaped := range []string{`\x1b[32m`, `\x1b[0m`, `\x1b[2J`, `\x1b]52`, `\u009b31m`, `\u009d52`, `\x80`, `\xff`} {
		if !strings.Contains(plain, escaped) {
			t.Errorf("missing inert representation %q in %q", escaped, plain)
		}
	}
}

func TestNonDisplayableUnicodeEscapes(t *testing.T) {
	for _, r := range []rune{0xAD, 0x034F, 0x061C, 0x115F, 0x1160, 0x200B, 0x200D, 0x202E, 0x2066, 0xFE0F, 0xE0100, 0x2800, 0xA0, 0x2028, 0x0378, 0xE000} {
		text := "+before" + string(r) + "after\n"
		plain := renderDiff(text, false)
		want := "+before" + runeEscape(r) + "after\n"
		if plain != want {
			t.Errorf("%U: got %q want %q", r, plain, want)
		}
		colored := renderDiff(text, true)
		if !strings.Contains(colored, "\x1b[97;41m"+runeEscape(r)+"\x1b[0m\x1b[32mafter") {
			t.Errorf("%U: missing escape styling or base restoration: %q", r, colored)
		}
		assertSafeRendered(t, colored, true)
	}
}

func TestConfusableGlyphAndBackground(t *testing.T) {
	for _, glyph := range []string{"а", "Α", "Ａ", "ℓ", "𝐀"} {
		text := "+before" + glyph + "after\n"
		if got := renderDiff(text, false); got != text {
			t.Errorf("plain confusable was replaced: %q", got)
		}
		colored := renderDiff(text, true)
		if !strings.Contains(colored, "\x1b[30;43m"+glyph+"\x1b[0m\x1b[32mafter") {
			t.Errorf("glyph missing or background not restored: %q", colored)
		}
		if got := assertSafeRendered(t, colored, true); got != text {
			t.Errorf("colored glyph replaced: %q", got)
		}
	}
	// ASCII confusables and ordinary international text are not blanket warnings.
	const ordinary = "+m a 0 café café 中文\n"
	rendered := renderDiff(ordinary, true)
	if strings.Contains(rendered, styleConfusable.sequence()) || strings.Contains(rendered, styleEscape.sequence()) {
		t.Fatalf("ordinary text was needlessly flagged: %q", rendered)
	}
	if got := assertSafeRendered(t, rendered, true); got != ordinary {
		t.Fatal(got)
	}
}

func TestWarningStyleRestoresEveryDiffStyle(t *testing.T) {
	for _, tc := range []struct{ prefix, restore string }{{"+", "\x1b[32m"}, {"-", "\x1b[31m"}, {"@@ ", "\x1b[36m"}, {" ", ""}} {
		got := renderDiff(tc.prefix+"aаz\n", true)
		want := "\x1b[30;43mа\x1b[0m" + tc.restore + "z"
		if !strings.Contains(got, want) {
			t.Errorf("%q: base style not restored: %q", tc.prefix, got)
		}
	}
	// Renderer-generated escapes have a warning background; literal source
	// spellings of those same escape sequences do not.
	got := renderDiff("+literal \\x1b vs \x1b end\n", true)
	if strings.Count(got, styleEscape.sequence()) != 1 {
		t.Fatalf("escaped source confused with generated escape: %q", got)
	}
}

func TestCombiningMarksCannotAlterDiffMarkers(t *testing.T) {
	for _, prefix := range []string{"+", "-", " "} {
		got := renderDiff(prefix+"\u0305text\n", true)
		if strings.ContainsRune(got, '\u0305') || !strings.Contains(got, `\u0305`) {
			t.Fatalf("orphan mark attached to trusted diff prefix: %q", got)
		}
	}
	// A visible cluster with a confusable mark remains intact, with the
	// background on its base as well (the mark itself has zero display width).
	got := renderDiff("+x\u0305z\n", true)
	if !strings.Contains(got, "\x1b[30;43mx\u0305\x1b[0m\x1b[32mz") {
		t.Fatalf("invisible mark-only highlighting: %q", got)
	}
	for _, text := range []string{"+\t\u0301x\n", "+\u200D\u0301x\n", "+\xff\u0301x\n"} {
		if got := renderDiff(text, false); strings.ContainsRune(got, '\u0301') {
			t.Fatalf("orphan mark after layout/escape: %q", got)
		}
	}
}

func TestLabelsAndErrorsUseSafeRendering(t *testing.T) {
	raw := "filename\x1b[31m\a\r\u009b\u034F\u200D\tа"
	plain := SafeText(raw)
	assertSafeRendered(t, plain, false)
	if strings.ContainsRune(plain, '\t') {
		t.Fatal("metadata tab wasn't escaped")
	}
	if !strings.ContainsRune(plain, 'а') {
		t.Fatal("confusable glyph escaped in diagnostic")
	}
	p := &prompt{color: true}
	got := p.styled(styleBold, raw)
	if assertSafeRendered(t, got, true) != plain {
		t.Fatal("label styling changed text")
	}
	if !strings.Contains(got, "\x1b[30;43mа") {
		t.Fatal("label confusable not highlighted")
	}
}

func FuzzSafeRenderer(f *testing.F) {
	for _, seed := range []string{"", "+\tGo\n", "+аΑ café 中文\n", "+\x1b]52;c;YQ==\a\n", "+\xff\xc0\x9b\u009b\n", "+x\u0305\u034F\u202E\n", "+\u0301\n", "@@ header\n-old\r\n+new\n"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, source string) {
		plain := renderDiff(source, false)
		assertSafeRendered(t, plain, false)
		colored := renderDiff(source, true)
		if got := assertSafeRendered(t, colored, true); got != plain {
			t.Fatalf("color changed content: %q != %q", got, plain)
		}
		assertSafeRendered(t, SafeText(source), false)
		quoted := (&prompt{}).quoted(source)
		assertSafeRendered(t, quoted, false)
		if strings.ContainsAny(quoted, "\n\t") {
			t.Fatalf("quoted field emitted raw layout: %q", quoted)
		}
		coloredQuote := (&prompt{color: true}).quoted(source)
		if got := assertSafeRendered(t, coloredQuote, true); got != quoted {
			t.Fatalf("color changed quoted field: %q != %q", got, quoted)
		}
	})
}

func TestRawFilenameRendering(t *testing.T) {
	for _, tc := range []struct{ path, want string }{
		{"\x1b\u0305", `"\x1b\u0305"`},
		{"\u0305file", `"\u0305file"`},
		{"\u0558", "\"\u0558\""}, // Unicode 18 assigned letter: no Go-version prequoting.
		{"\u200D", `"\u200d"`},
		{"\u034F", `"\u034f"`},
		{"line\nname\t\"\\", `"line\nname\t\"\\"`},
		{"а\xff", "\"а\\xff\""},
	} {
		t.Run(tc.want, func(t *testing.T) {
			for _, color := range []bool{false, true} {
				p := &prompt{color: color}
				got := p.fileHeading(tc.path, "added", "undecided")
				want := "--- " + tc.want + " (added) [undecided]"
				if plain := assertSafeRendered(t, got, color); plain != want {
					t.Fatalf("heading: %q, want %q", plain, want)
				}
				if strings.ContainsRune(got, '\u0305') {
					t.Fatalf("mark attached to quote/generated escape: %q", got)
				}
			}
			s := sample()
			s.Files[0].Path = tc.path
			var out strings.Builder
			if err := Run(s, strings.NewReader("q\n"), &out, ""); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "--- "+tc.want+" (modified)") {
				t.Fatalf("header callsite bypassed raw renderer: %q", out.String())
			}
			assertSafeRendered(t, out.String(), false)
		})
	}
	p := &prompt{color: true}
	if got := p.fileHeading("\u200Dа", "added", "undecided"); !strings.Contains(got, "\x1b[97;41m\\u200d\x1b[0m\x1b[30;43mа") {
		t.Fatalf("filename warning provenance lost: %q", got)
	}
}

func TestUnknownCommandUsesPinnedEscaping(t *testing.T) {
	var out strings.Builder
	if err := Run(sample(), strings.NewReader("\u034f\ufe0f\u2800\nq\n"), &out, ""); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	assertSafeRendered(t, got, false)
	if !strings.Contains(got, `Unknown command "\u034f\ufe0f\u2800"`) {
		t.Fatalf("unescaped command: %q", got)
	}
}
