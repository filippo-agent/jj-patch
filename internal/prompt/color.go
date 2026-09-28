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

// The caller must sanitize untrusted text before applying our own ANSI styles.
func (p *prompt) styled(code, safeText string) string {
	if !p.color || safeText == "" {
		return safeText
	}
	return "\x1b[" + code + "m" + safeText + "\x1b[0m"
}

func (p *prompt) showDiff(text string) {
	for _, line := range strings.SplitAfter(text, "\n") {
		if line == "" {
			continue
		}
		hasNewline := strings.HasSuffix(line, "\n")
		content := printableDiff(strings.TrimSuffix(line, "\n"))
		switch {
		case strings.HasPrefix(line, "@@"):
			content = p.styled("36", content)
		case strings.HasPrefix(line, "+"):
			content = p.styled("32", content)
		case strings.HasPrefix(line, "-"):
			content = p.styled("31", content)
		}
		p.printf("%s", content)
		if hasNewline {
			p.printf("\n")
		}
	}
	if !strings.HasSuffix(text, "\n") {
		p.printf("\n")
	}
}
