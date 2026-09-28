package prompt

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Only these renderer-owned styles can produce terminal escape sequences.
// File text is decoded and classified before it reaches emit.
type textStyle uint8

const (
	stylePlain textStyle = iota
	styleBold
	stylePrompt
	styleAddition
	styleDeletion
	styleHunk
	styleEscape
	styleConfusable
)

func (s textStyle) sequence() string {
	switch s {
	case styleBold:
		return "\x1b[1m"
	case stylePrompt:
		return "\x1b[1;33m"
	case styleAddition:
		return "\x1b[32m"
	case styleDeletion:
		return "\x1b[31m"
	case styleHunk:
		return "\x1b[36m"
	case styleEscape:
		return "\x1b[97;41m"
	case styleConfusable:
		return "\x1b[30;43m"
	default:
		return "\x1b[0m"
	}
}

type textRenderer struct {
	out          strings.Builder
	color        bool
	base, active textStyle
}

// emit takes already classified, safe text. A warning's background/foreground
// never leaks into the remainder of a diff line: restore its complete base style.
func (r *textRenderer) emit(text string, warning textStyle) {
	if text == "" {
		return
	}
	if r.color {
		wanted := r.base
		if warning != stylePlain {
			wanted = warning
		}
		if wanted != r.active {
			if r.active != stylePlain {
				r.out.WriteString(stylePlain.sequence())
			}
			if wanted != stylePlain {
				r.out.WriteString(wanted.sequence())
			}
			r.active = wanted
		}
	}
	r.out.WriteString(text)
}

func (r *textRenderer) finish() string {
	if r.active != stylePlain {
		r.out.WriteString(stylePlain.sequence())
		r.active = stylePlain
	}
	return r.out.String()
}

func runeEscape(r rune) string {
	quoted := strconv.QuoteRuneToASCII(r)
	return quoted[1 : len(quoted)-1]
}

type scanOptions struct {
	tabs, newlines, quoted bool
}

func (r *textRenderer) scan(s string, allowTabs bool) {
	r.scanWithOptions(s, scanOptions{tabs: allowTabs, newlines: true})
}

func (r *textRenderer) quoted(s string) {
	r.emit(`"`, stylePlain)
	// Scan raw bytes separately from the trusted quotes. Prequoting would lose
	// escape provenance and let a leading mark attach to the opening quote.
	r.scanWithOptions(s, scanOptions{quoted: true})
	r.emit(`"`, stylePlain)
}

func (r *textRenderer) scanWithOptions(s string, options scanOptions) {
	for len(s) > 0 {
		ch, size := utf8.DecodeRuneInString(s)
		switch {
		case ch == utf8.RuneError && size == 1:
			r.emit(fmt.Sprintf("\\x%02x", s[0]), styleEscape)
		case (options.newlines && ch == '\n') || (options.tabs && ch == '\t'):
			// These two layout characters are deliberate exceptions. ESC, CR, C1,
			// OSC/DCS terminators and all other controls can never pass through.
			r.emit(s[:size], stylePlain)
		case options.quoted && (ch == '"' || ch == '\\'):
			// Ordinary quoting syntax is not a Unicode/control warning.
			r.emit("\\"+s[:size], stylePlain)
		case isNonDisplayable(ch) || isCombiningMark(ch):
			// An orphan mark must not attach to a diff marker, generated escape, or
			// preceding line. Marks attached to a real base are consumed below.
			r.emit(runeEscape(ch), styleEscape)
		default:
			end := size
			confusable := isConfusable(ch)
			for end < len(s) {
				mark, n := utf8.DecodeRuneInString(s[end:])
				if (mark == utf8.RuneError && n == 1) || !isCombiningMark(mark) || isNonDisplayable(mark) {
					break
				}
				confusable = confusable || isConfusable(mark)
				end += n
			}
			warning := stylePlain
			if confusable {
				warning = styleConfusable
			}
			// A zero-width confusable mark gets a visible background by highlighting
			// its entire base-plus-marks cluster, without replacing any of its glyphs.
			r.emit(s[:end], warning)
			size = end
		}
		s = s[size:]
	}
}

// SafeText escapes unsafe/non-displayable text without emitting any ANSI. It is
// also used for errors whose filenames or subprocess messages may be untrusted.
// Newlines are retained; metadata tabs are escaped rather than used as layout.
func SafeText(s string) string {
	var r textRenderer
	r.scan(s, false)
	return r.finish()
}

func printable(s string) string     { return SafeText(s) }
func printableDiff(s string) string { return renderDiff(s, false) }

func (p *prompt) quoted(s string) string {
	r := textRenderer{color: p.color}
	r.quoted(s)
	return r.finish()
}

func (p *prompt) fileHeading(path, kind, state string) string {
	r := textRenderer{color: p.color, base: styleBold}
	r.emit("--- ", stylePlain)
	r.quoted(path)
	if kind != "" {
		r.emit(" (", stylePlain)
		r.scanWithOptions(kind, scanOptions{})
		r.emit(")", stylePlain)
	}
	r.emit(" [", stylePlain)
	r.scanWithOptions(state, scanOptions{})
	r.emit("]", stylePlain)
	return r.finish()
}

func renderDiff(text string, color bool) string {
	var out strings.Builder
	for _, line := range strings.SplitAfter(text, "\n") {
		if line == "" {
			continue
		}
		content := strings.TrimSuffix(line, "\n")
		r := textRenderer{color: color}
		switch {
		case strings.HasPrefix(content, "@@"):
			r.base = styleHunk
		case strings.HasPrefix(content, "+"):
			r.base = styleAddition
		case strings.HasPrefix(content, "-"):
			r.base = styleDeletion
		}
		// Treat the diff marker as structure, not a base for a file's leading
		// combining mark. Otherwise that mark could visually alter '+' or '-'.
		if len(content) > 0 && strings.ContainsRune("+- ", rune(content[0])) {
			r.emit(content[:1], stylePlain)
			content = content[1:]
		}
		r.scan(content, true)
		out.WriteString(r.finish())
		if strings.HasSuffix(line, "\n") {
			out.WriteByte('\n')
		}
	}
	return out.String()
}
