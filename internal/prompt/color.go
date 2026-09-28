package prompt

import (
	"io"
	"os"
	"strings"
)

// Only real terminals get ANSI color, never pipes, files, or ordinary capture
// writers. NO_COLOR's presence (even an empty value) explicitly disables it.
// Writers wrapping a terminal can opt into detection by forwarding Fd().
func colorEnabled(out io.Writer) bool {
	if _, disabled := os.LookupEnv("NO_COLOR"); disabled || os.Getenv("TERM") == "dumb" {
		return false
	}
	fd, ok := out.(interface{ Fd() uintptr })
	return ok && isTerminal(fd.Fd())
}

// Styling never bypasses sanitization, even for labels and filenames.
func (p *prompt) styled(style textStyle, text string) string {
	r := textRenderer{color: p.color, base: style}
	r.scan(text, false)
	return r.finish()
}

func (p *prompt) showDiff(text string) {
	p.printf("%s", renderDiff(text, p.color))
	if !strings.HasSuffix(text, "\n") {
		p.printf("\n")
	}
}
