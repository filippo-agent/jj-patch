// Package prompt provides a line-oriented, git add -p style selector.
// It does not apply patches or write to the repository.
package prompt

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/filippo-agent/jj-patch-interactive/internal/edit"
)

// ErrAbort means that the user canceled, or input ended before selection was
// complete. The caller must not apply the session in this case.
var ErrAbort = errors.New("patch selection aborted")

type position struct{ file, hunk int }

// commandInput prevents bufio from reading beyond the current command's
// newline. jj can invoke this editor repeatedly on one shared stdin, and e
// hands that same input to an external editor. Neither may lose future input
// to a buffer belonging to an earlier command parser.
type commandInput struct{ io.Reader }

func (in commandInput) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return in.Reader.Read(p)
}

type output struct {
	io.Writer
	err error
}

func (w *output) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	n, err := w.Writer.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	w.err = err
	return n, err
}

type prompt struct {
	session *edit.Session
	decided [][]bool
	input   io.Reader
	reader  *bufio.Reader
	out     *output
	context string
	current position
	filter  *regexp.Regexp
	color   bool
}

// Run selects hunks in s, committing its in-memory changes only on success.
// An initially Selected hunk is already decided; an unselected hunk is initially
// undecided. Rejected hunks are tracked independently of Selected during Run.
//
// q preserves every existing decision and skips all still-undecided hunks,
// including those hidden by a filter. Q and premature EOF return ErrAbort and
// leave s unchanged. Finishing all decisions also returns success.
//
// Filters are a global view, not a modification of the session. If undecided
// hunks remain hidden, Run waits for another command (G to clear, or q to save),
// rather than silently losing the opportunity to review them.
func Run(s *edit.Session, in io.Reader, out io.Writer, context string) error {
	if s == nil {
		return errors.New("nil edit session")
	}
	work := clone(s)
	p := &prompt{
		session: &work,
		input:   in,
		reader:  bufio.NewReader(commandInput{in}),
		out:     &output{Writer: out},
		context: strings.TrimSpace(context),
		current: position{-1, -1},
		decided: make([][]bool, len(work.Files)),
		color:   colorEnabled(out),
	}
	for f := range work.Files {
		p.decided[f] = make([]bool, len(work.Files[f].Hunks))
		for h := range work.Files[f].Hunks {
			p.decided[f][h] = work.Files[f].Hunks[h].Selected
		}
	}
	if err := p.run(); err != nil {
		return err
	}
	if p.out.err != nil {
		return p.out.err
	}
	*s = work
	return nil
}

func clone(s *edit.Session) edit.Session {
	// Clone isolates public choices AND private split/edit bookkeeping. The UI
	// never closes a temporary clone: shared diff resources remain owned by the
	// caller, which closes s after Run (whether committed or aborted).
	return *s.Clone()
}

func (p *prompt) printf(format string, args ...any) {
	fmt.Fprintf(p.out, format, args...)
}

func (p *prompt) readLine() (string, error) {
	if p.out.err != nil {
		return "", p.out.err
	}
	// ReadString, unlike Scanner's default, accepts arbitrarily long commands.
	// A partial line at EOF is not an implicit confirmation.
	line, err := p.reader.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			return "", ErrAbort
		}
		return "", fmt.Errorf("read patch command: %w", err)
	}
	return strings.TrimSpace(line), nil
}

func (p *prompt) remaining() bool {
	for _, file := range p.decided {
		for _, decided := range file {
			if !decided {
				return true
			}
		}
	}
	return false
}

func (p *prompt) visible() []position {
	var result []position
	for f, file := range p.session.Files {
		for h := range file.Hunks {
			if p.filter == nil || p.matches(p.filter, position{f, h}) {
				result = append(result, position{f, h})
			}
		}
	}
	return result
}

func (p *prompt) matches(re *regexp.Regexp, pos position) bool {
	file := p.session.Files[pos.file]
	return re.MatchString(file.Path) || re.MatchString(file.Hunks[pos.hunk].Text)
}

func indexOf(positions []position, pos position) int {
	for i, candidate := range positions {
		if candidate == pos {
			return i
		}
	}
	return -1
}

func (p *prompt) chooseCurrent(visible []position) {
	if indexOf(visible, p.current) >= 0 {
		return
	}
	p.current = position{-1, -1}
	if len(visible) > 0 {
		p.current = visible[0]
		for _, pos := range visible {
			if !p.decided[pos.file][pos.hunk] {
				p.current = pos
				break
			}
		}
	}
}

func (p *prompt) decide(pos position, selected bool) {
	p.decided[pos.file][pos.hunk] = true
	p.session.Files[pos.file].Hunks[pos.hunk].Selected = selected
}

// Advance wraps to earlier skipped hunks rather than treating them as rejected.
func (p *prompt) advance() {
	visible := p.visible()
	index := indexOf(visible, p.current)
	for step := 1; step <= len(visible); step++ {
		pos := visible[(index+step)%len(visible)]
		if !p.decided[pos.file][pos.hunk] {
			p.current = pos
			return
		}
	}
}

func (p *prompt) save() {
	for f, file := range p.decided {
		for h, decided := range file {
			if !decided {
				p.decide(position{f, h}, false)
			}
		}
	}
}

func (p *prompt) show(visible []position) {
	if p.filter != nil {
		p.printf("Global filter: %s\n", printable(p.filter.String()))
	}
	index := indexOf(visible, p.current)
	if index < 0 {
		p.printf("No hunks match the filter. Use G then an empty line to clear it, or q to save.\n")
		p.printf("Command [G,S,A,q,Q,?]? ")
		return
	}
	file := p.session.Files[p.current.file]
	hunk := file.Hunks[p.current.hunk]
	state := "undecided"
	if p.decided[p.current.file][p.current.hunk] {
		state = "excluded"
		if hunk.Selected {
			state = "included"
		}
	}
	header := "--- " + strconv.Quote(file.Path)
	if file.Kind != "" {
		header += " (" + printable(file.Kind) + ")"
	}
	header += " [" + state + "]"
	p.printf("\n%s\n", p.styled("1", header))
	p.showDiff(hunk.Text)
	hasUndecided := false
	for _, pos := range visible {
		hasUndecided = hasUndecided || !p.decided[pos.file][pos.hunk]
	}
	if !hasUndecided && p.remaining() {
		p.printf("All visible hunks decided; hidden hunks remain. Clear G to review them, or q to save.\n")
	}
	context := ""
	if p.context != "" {
		context = " " + printable(p.context)
	}
	p.printf("(%d/%d) %s [y,n,q,Q,a,d,A,s,S,e,j,k,J,K,g,/,G,?] ",
		index+1, len(visible), p.styled("1;33", "Include this hunk"+context+"?"))
}

func (p *prompt) run() error {
	for p.remaining() {
		visible := p.visible()
		p.chooseCurrent(visible)
		p.show(visible)
		line, err := p.readLine()
		if err != nil {
			return err
		}
		if line == "" {
			continue
		}
		// Commands are deliberately case-sensitive. In particular, g != G,
		// j/k != J/K, and q != Q.
		command, argument := line, ""
		if strings.ContainsAny(line[:1], "gG/") {
			command, argument = line[:1], strings.TrimSpace(line[1:])
		}
		switch command {
		case "q":
			p.save()
			return nil
		case "Q":
			return ErrAbort
		case "?":
			p.printf("%s", help)
		case "G", "/", "g":
			if argument == "" {
				questions := map[string]string{
					"G": "Global filter (regex; empty clears)? ",
					"/": "Search visible hunks (regex)? ",
					"g": "Go to which visible hunk number? ",
				}
				p.printf("%s", questions[command])
				argument, err = p.readLine()
				if err != nil {
					return err
				}
			}
			p.navigate(command, argument, visible)
		case "S":
			count := 0
			for f := range p.session.Files {
				for h := 0; h < len(p.session.Files[f].Hunks); {
					if p.split(position{f, h}) {
						count++
					} else {
						h++
					}
				}
			}
			p.printf("Split all hunks: %d split(s).\n", count)
		case "A":
			for _, pos := range visible {
				if !p.decided[pos.file][pos.hunk] {
					p.decide(pos, true)
				}
			}
			// Like q, A explicitly finishes the session, but first accepts
			// every remaining visible hunk. Hidden hunks are never selected.
			p.save()
			return nil
		default:
			if indexOf(visible, p.current) < 0 {
				p.printf("No visible hunk. Use G to change or clear the filter.\n")
				continue
			}
			switch command {
			case "y", "n":
				p.decide(p.current, command == "y")
				p.advance()
			case "a", "d":
				for _, pos := range visible {
					if pos.file == p.current.file && pos.hunk >= p.current.hunk &&
						(pos == p.current || !p.decided[pos.file][pos.hunk]) {
						p.decide(pos, command == "a")
					}
				}
				p.advance()
			case "j", "k", "J", "K":
				p.move(command, visible)
			case "s":
				if !p.split(p.current) {
					p.printf("Cannot split this hunk further.\n")
				}
			case "e":
				ok, err := p.editCurrent()
				if err != nil {
					return err
				}
				if ok {
					p.decide(p.current, true)
					p.advance()
				}
			default:
				p.printf("Unknown command %s. Type ? for help.\n", strconv.Quote(line))
			}
		}
	}
	return p.out.err
}

func (p *prompt) navigate(command, argument string, visible []position) {
	if command == "g" {
		number, err := strconv.Atoi(argument)
		if err != nil || number < 1 || number > len(visible) {
			p.printf("Invalid hunk number; choose 1 through %d.\n", len(visible))
		} else {
			p.current = visible[number-1]
		}
		return
	}
	if command == "G" && argument == "" {
		p.filter = nil
		p.printf("Global filter cleared.\n")
		return
	}
	if argument == "" {
		return
	}
	re, err := regexp.Compile(argument)
	if err != nil {
		p.printf("Invalid regex: %s\n", printable(err.Error()))
		return // In particular, leave an existing global filter untouched.
	}
	if command == "G" {
		p.filter = re
		return
	}
	index := indexOf(visible, p.current)
	for step := 1; step <= len(visible); step++ {
		pos := visible[(index+step)%len(visible)]
		if p.matches(re, pos) {
			p.current = pos
			return
		}
	}
	p.printf("Pattern not found: %s\n", printable(argument))
}

func (p *prompt) move(command string, visible []position) {
	direction := 1
	if command == "k" || command == "K" {
		direction = -1
	}
	anyHunk := command == "J" || command == "K"
	for i := indexOf(visible, p.current) + direction; i >= 0 && i < len(visible); i += direction {
		pos := visible[i]
		if anyHunk || !p.decided[pos.file][pos.hunk] {
			p.current = pos
			return
		}
	}
	p.printf("No %s hunk in that direction.\n", map[bool]string{true: "visible", false: "undecided"}[anyHunk])
}

func (p *prompt) split(pos position) bool {
	before := len(p.session.Files[pos.file].Hunks)
	selected := p.session.Files[pos.file].Hunks[pos.hunk].Selected
	decided := p.decided[pos.file][pos.hunk]
	if !p.session.Split(pos.file, pos.hunk) {
		return false
	}
	children := len(p.session.Files[pos.file].Hunks) - before + 1
	if children <= 1 {
		// The engine's Split contract requires replacement by multiple hunks.
		// Do not loop forever if an implementation reports a no-op as success.
		return false
	}
	old := p.decided[pos.file]
	next := make([]bool, len(old)+children-1)
	copy(next, old[:pos.hunk])
	for h := pos.hunk; h < pos.hunk+children; h++ {
		next[h] = decided
		p.session.Files[pos.file].Hunks[h].Selected = selected
	}
	copy(next[pos.hunk+children:], old[pos.hunk+1:])
	p.decided[pos.file] = next
	if p.current.file == pos.file && p.current.hunk > pos.hunk {
		p.current.hunk += children - 1
	}
	return true
}

// printable preserves line structure, but never emits terminal control codes,
// invalid UTF-8, or invisible formatting controls from a patch or an error.
func printable(s string) string {
	var result strings.Builder
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		if r == utf8.RuneError && size == 1 {
			fmt.Fprintf(&result, "\\x%02x", s[0])
		} else if r == '\n' || unicode.IsPrint(r) {
			result.WriteRune(r)
		} else {
			quoted := strconv.QuoteRuneToASCII(r)
			result.WriteString(quoted[1 : len(quoted)-1])
		}
		s = s[size:]
	}
	return result.String()
}

const help = `y - include this hunk (also changes an earlier decision)
n - exclude this hunk (also changes an earlier decision)
q - save decisions; skip ALL undecided hunks, including hidden ones
Q - abort without saving any changes (EOF also aborts)
a/d - include/exclude this and later undecided visible hunks in this file
A - include all undecided visible hunks across all files, then save
s - split this hunk, preserving its decision
S - split all hunks globally, preserving every decision
e - edit in $VISUAL or $EDITOR; a valid edit includes it, an empty edit cancels
j/k - next/previous undecided visible hunk
J/K - next/previous visible hunk, including decided hunks
g [number] - go to a visible hunk number (across all files)
/ [regex] - search visible hunk text or paths, wrapping around
G [regex] - globally filter hunk text or paths; G then empty clears
? - show this help
Filters hide hunks without changing decisions. Clear G to review hidden hunks.
`
