package edit

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The context-boundary splitting strategy is adapted from
// github.com/cwarden/git-add--interactive/internal/git/patch.go,
// Copyright (c) 2025 Christian G. Warden, MIT (see the project LICENSE).
// Unlike applying overlapping patches sequentially, our changes are anchored to
// the immutable old lines, so overlapping split context cannot consume edits.
func (s *Session) Split(file, hunk int) bool {
	hs, err := s.hunk(file, hunk)
	if err != nil || hs.metadata {
		return false
	}
	public := s.Files[file].Hunks[hunk]
	if public.Text != hs.text {
		return false
	}
	p, err := parseHunk(hs.text, true)
	if err != nil {
		return false
	}
	changed := false
	point := -1
	for i, r := range p.lines {
		if r.kind != ' ' {
			changed = true
			continue
		}
		if changed {
			for _, later := range p.lines[i+1:] {
				if later.kind != ' ' {
					point = i
					break
				}
			}
			if point >= 0 {
				break
			}
		}
	}
	if point < 0 {
		return false
	}
	a := parsedHunk{start: p.start, newStart: p.newStart, lines: p.lines[:point+1]}
	b := parsedHunk{start: p.start + a.oldCount() - 1, newStart: p.newStart + a.newCount() - 1, lines: p.lines[point:]}
	// Old/new coordinates are represented internally as zero-based boundaries,
	// including pure additions and removals (unified diff's zero-count exception).
	texts := []string{a.render(), b.render()}
	states := []hunkState{{text: texts[0], start: a.start, count: a.oldCount()}, {text: texts[1], start: b.start, count: b.oldCount()}}
	pubs := []Hunk{{Text: texts[0], Selected: public.Selected}, {Text: texts[1], Selected: public.Selected}}
	old := s.states[file].hunks
	s.states[file].hunks = append(append(append([]hunkState{}, old[:hunk]...), states...), old[hunk+1:]...)
	oldpub := s.Files[file].Hunks
	s.Files[file].Hunks = append(append(append([]Hunk{}, oldpub[:hunk]...), pubs...), oldpub[hunk+1:]...)
	return true
}

// Edit replaces a text hunk, validates it against the immutable baseline, and
// recounts header lengths. The old start and span must remain the same: to keep
// a removed line, change '-' to ' '; to omit an addition, remove its '+' line.
// Metadata, binary and symlink hunks cannot be edited. Selection is unchanged.
// An error leaves both text and selection unchanged.
func (s *Session) Edit(file, hunk int, text string) error {
	hs, err := s.hunk(file, hunk)
	if err != nil {
		return err
	}
	if hs.metadata {
		return fmt.Errorf("metadata/binary/symlink changes are indivisible and cannot be edited")
	}
	p, err := parseHunk(text, false)
	if err != nil {
		return err
	}
	if p.start != hs.start || p.oldCount() != hs.count {
		return fmt.Errorf("edited hunk must preserve its original old-line start and span")
	}
	var base []byte
	if old := s.states[file].old; old != nil {
		base = old.data
	}
	ops, err := p.operations(base)
	if err != nil {
		return err
	}
	if _, err = applyOperations(base, ops); err != nil {
		return err
	}
	normalized := p.render()
	hs.text = normalized
	s.Files[file].Hunks[hunk].Text = normalized
	return nil
}

func (s *Session) diff(old, new []byte) ([]string, error) {
	if err := os.WriteFile(filepath.Join(s.temp, "old"), old, 0600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(s.temp, "new"), new, 0600); err != nil {
		return nil, err
	}
	// No caller GIT_* variables, HOME config, attributes, repository discovery,
	// pager, editor, external diff, textconv, filters, or index are involved.
	cmd := exec.Command("git", "diff", "--no-index", "--text", "--no-color", "--no-ext-diff", "--no-textconv", "--no-renames", "--diff-algorithm=myers", "--unified=3", "--inter-hunk-context=0", "--", "old", "new")
	cmd.Dir = s.temp
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + s.temp, "XDG_CONFIG_HOME=" + s.temp, "LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_SYSTEM=" + os.DevNull, "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_ATTR_NOSYSTEM=1", "GIT_CONFIG_COUNT=0", "GIT_CEILING_DIRECTORIES=" + s.temp, "GIT_OPTIONAL_LOCKS=0"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
			return nil, fmt.Errorf("git diff: %w: %s", err, stderr.String())
		}
	}
	var out []string
	var current strings.Builder
	for _, line := range strings.SplitAfter(string(data), "\n") {
		if strings.HasPrefix(line, "@@ ") {
			if current.Len() > 0 {
				out = append(out, current.String())
				current.Reset()
			}
			current.WriteString(line)
		} else if current.Len() > 0 {
			current.WriteString(line)
		}
	}
	if current.Len() > 0 {
		out = append(out, current.String())
	}
	if len(out) == 0 && !bytes.Equal(old, new) {
		return nil, fmt.Errorf("git produced no text hunks")
	}
	return out, nil
}

type diffLine struct {
	kind byte
	data string
}
type parsedHunk struct {
	start, newStart int
	lines           []diffLine
}

var headerRE = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(?:.*)$`)

func parseHunk(text string, strict bool) (parsedHunk, error) {
	p := parsedHunk{}
	if !strings.HasSuffix(text, "\n") {
		return p, fmt.Errorf("hunk must end with a newline (use the no-newline marker for file data)")
	}
	raw := strings.SplitAfter(text, "\n")
	m := headerRE.FindStringSubmatch(strings.TrimSuffix(raw[0], "\n"))
	if m == nil {
		return p, fmt.Errorf("invalid unified hunk header")
	}
	nums := []int{0, 1, 0, 1}
	for i := range nums {
		if m[i+1] != "" {
			n, e := strconv.Atoi(m[i+1])
			if e != nil {
				return p, fmt.Errorf("invalid hunk coordinate")
			}
			nums[i] = n
		}
	}
	p.start = nums[0]
	if nums[1] > 0 {
		if p.start == 0 {
			return p, fmt.Errorf("invalid old-line coordinate")
		}
		p.start--
	}
	p.newStart = nums[2]
	if nums[3] > 0 {
		if p.newStart == 0 {
			return p, fmt.Errorf("invalid new-line coordinate")
		}
		p.newStart--
	}
	for _, line := range raw[1 : len(raw)-1] {
		if line == "\\ No newline at end of file\n" {
			if len(p.lines) == 0 || !strings.HasSuffix(p.lines[len(p.lines)-1].data, "\n") {
				return p, fmt.Errorf("misplaced no-newline marker")
			}
			p.lines[len(p.lines)-1].data = strings.TrimSuffix(p.lines[len(p.lines)-1].data, "\n")
			continue
		}
		if len(line) < 2 || (line[0] != ' ' && line[0] != '+' && line[0] != '-') {
			return p, fmt.Errorf("invalid hunk body line (only context, +, - and no-newline markers are allowed)")
		}
		if strings.IndexByte(line, 0) >= 0 {
			return p, fmt.Errorf("NUL bytes require an indivisible binary change")
		}
		p.lines = append(p.lines, diffLine{kind: line[0], data: line[1:]})
	}
	if len(p.lines) == 0 && nums[1] != 0 {
		return p, fmt.Errorf("empty hunk")
	}
	if strict && (p.oldCount() != nums[1] || p.newCount() != nums[3]) {
		return p, fmt.Errorf("hunk line counts do not match header")
	}
	return p, nil
}
func (p parsedHunk) oldCount() int {
	n := 0
	for _, r := range p.lines {
		if r.kind != '+' {
			n++
		}
	}
	return n
}
func (p parsedHunk) newCount() int {
	n := 0
	for _, r := range p.lines {
		if r.kind != '-' {
			n++
		}
	}
	return n
}
func (p parsedHunk) render() string {
	var b strings.Builder
	a, z := p.start, p.newStart
	ac, zc := p.oldCount(), p.newCount()
	if ac > 0 {
		a++
	}
	if zc > 0 {
		z++
	}
	fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", a, ac, z, zc)
	for _, r := range p.lines {
		b.WriteByte(r.kind)
		b.WriteString(r.data)
		if !strings.HasSuffix(r.data, "\n") {
			b.WriteString("\n\\ No newline at end of file\n")
		}
	}
	return b.String()
}
func lines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	v := strings.SplitAfter(string(data), "\n")
	if v[len(v)-1] == "" {
		v = v[:len(v)-1]
	}
	return v
}

type operation struct {
	start, end  int
	replacement []string
}

func (p parsedHunk) operations(base []byte) ([]operation, error) {
	original := lines(base)
	pos := p.start
	if pos < 0 || pos > len(original) {
		return nil, fmt.Errorf("hunk starts outside baseline")
	}
	var ops []operation
	var pending *operation
	flush := func() {
		if pending != nil {
			ops = append(ops, *pending)
			pending = nil
		}
	}
	for _, r := range p.lines {
		if r.kind != '+' {
			if pos >= len(original) || original[pos] != r.data {
				return nil, fmt.Errorf("hunk does not match baseline at old line %d", pos+1)
			}
		}
		if r.kind == ' ' {
			flush()
			pos++
			continue
		}
		if pending == nil {
			pending = &operation{start: pos, end: pos}
		}
		if r.kind == '-' {
			pos++
			pending.end = pos
		} else {
			pending.replacement = append(pending.replacement, r.data)
		}
	}
	flush()
	return ops, nil
}
func applyOperations(base []byte, ops []operation) ([]byte, error) {
	sort.SliceStable(ops, func(i, j int) bool { return ops[i].start < ops[j].start })
	original := lines(base)
	var output []string
	pos := 0
	for i, op := range ops {
		if op.start < pos || op.end > len(original) || op.start > op.end {
			return nil, fmt.Errorf("selected changes overlap or extend outside baseline")
		}
		if i > 0 && op.start == ops[i-1].start {
			return nil, fmt.Errorf("selected insertions overlap")
		}
		output = append(output, original[pos:op.start]...)
		output = append(output, op.replacement...)
		pos = op.end
	}
	output = append(output, original[pos:]...)
	for i, line := range output {
		if i < len(output)-1 && !strings.HasSuffix(line, "\n") {
			return nil, fmt.Errorf("selected changes place content after a line with no newline")
		}
	}
	return []byte(strings.Join(output, "")), nil
}
