// Package edit selects a delta between two directory snapshots. It never uses
// the caller's Git repository or index. Only Write publishes filesystem changes.
package edit

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Hunk is a selectable unified-diff hunk, or an indivisible metadata change.
// Call Edit to change Text; callers may freely change Selected.
type Hunk struct {
	Text     string
	Selected bool
}

// File is one changed pathname. Renames are represented as deletion + addition.
// Kind is "added", "deleted", "modified", or "typechange". Path is a raw native
// pathname, not Git-quoted text; it may contain non-UTF-8 bytes.
type File struct {
	Path  string
	Kind  string
	Hunks []Hunk
}

// Session owns immutable in-memory snapshots and private temporary diff files.
// It is not safe for concurrent use. Files' structure, Path and Kind are read-only.
type Session struct {
	Files                []File
	Instructions         string
	left, right, initial tree
	output               string
	outputInfo           os.FileInfo
	temp                 string
	states               []fileState
	closed               bool
	instructions         bool
	// publish is a test seam. It must either publish the entire directory or leave
	// the destination unchanged on error.
	publish func(string, string) error
}
type entry struct {
	data []byte
	mode os.FileMode
	link bool
}
type tree map[string]entry
type fileState struct {
	path, kind string
	old, new   *entry
	hunks      []hunkState
}
type hunkState struct {
	text         string
	metadata     bool
	modeOnly     bool
	start, count int
}

// Options controls recognition of jj-generated instruction files. Disable
// Instructions when a real added JJ-INSTRUCTIONS file imitates jj's prose.
type Options struct {
	Instructions bool
}

// Clone makes an independent in-memory working selection, including private
// split/edit bookkeeping. Immutable snapshots are shared. This lets a UI edit a
// tentative session and discard it on cancellation. A successful Write through
// either clone invalidates the other's output snapshot until the UI adopts the
// successful clone. Temporary diff resources may be closed through either copy.
func (s *Session) Clone() *Session {
	clone := *s
	clone.Files = append([]File(nil), s.Files...)
	for i := range clone.Files {
		clone.Files[i].Hunks = append([]Hunk(nil), s.Files[i].Hunks...)
	}
	clone.states = append([]fileState(nil), s.states...)
	for i := range clone.states {
		clone.states[i].hunks = append([]hunkState(nil), s.states[i].hunks...)
	}
	return &clone
}

// Open snapshots existing, non-overlapping directory trees. Empty output means
// right; otherwise output must be an existing third directory (as supplied by
// jj). No selection is initially enabled. Close without Write is cancellation.
func Open(left, right, output string) (*Session, error) {
	return OpenWithOptions(left, right, output, Options{Instructions: true})
}

// OpenWithOptions is Open with explicit instruction-file recognition policy.
// With Instructions false, every JJ-INSTRUCTIONS file is an ordinary file.
func OpenWithOptions(left, right, output string, options Options) (_ *Session, err error) {
	left, err = cleanRoot(left)
	if err != nil {
		return nil, err
	}
	right, err = cleanRoot(right)
	if err != nil {
		return nil, err
	}
	if output == "" {
		output = right
	} else {
		output, err = cleanRoot(output)
		if err != nil {
			return nil, err
		}
	}
	if overlaps(left, right) || overlaps(left, output) || (right != output && overlaps(right, output)) {
		return nil, fmt.Errorf("snapshot directories must not overlap")
	}
	s := &Session{output: output, publish: publishDirectory, instructions: options.Instructions}
	defer func() {
		if err != nil {
			s.Close()
		}
	}()
	if s.left, err = snapshot(left); err != nil {
		return nil, fmt.Errorf("left: %w", err)
	}
	if s.right, err = snapshot(right); err != nil {
		return nil, fmt.Errorf("right: %w", err)
	}
	if s.initial, err = snapshot(output); err != nil {
		return nil, fmt.Errorf("output: %w", err)
	}
	s.outputInfo, err = os.Lstat(output)
	if err != nil {
		return nil, err
	}
	// When JJ-INSTRUCTIONS itself was deleted, jj may create its help at that
	// now-vacant path and unconditionally remove it after this tool exits. It
	// would also remove a real file restored by rejecting the deletion. The
	// protocol cannot distinguish that case from instruction-like user data:
	// fail closed and ask the caller to disable instructions before checkout.
	if s.instructions {
		candidate, hasCandidate := s.right[instructionsName]
		initial, hasInitial := s.initial[instructionsName]
		looksSynthetic := (hasCandidate && !candidate.link && IsInstructions(candidate.data)) ||
			(hasInitial && !initial.link && IsInstructions(initial.data))
		if looksSynthetic {
			for path := range s.left {
				if path == instructionsName || strings.HasPrefix(path, instructionsName+string(filepath.Separator)) {
					return nil, fmt.Errorf("JJ-INSTRUCTIONS collides with a versioned path; retry with jj's ui.diff-instructions=false and use --no-instructions for instruction-like user data")
				}
			}
		}
	}
	// Only a root regular file absent from the baseline is eligible.
	if _, tracked := s.left[instructionsName]; s.instructions && !tracked {
		if e, ok := s.right[instructionsName]; ok && !e.link && IsInstructions(e.data) {
			s.Instructions = string(e.data)
			delete(s.right, instructionsName)
		}
		if e, ok := s.initial[instructionsName]; ok && !e.link && IsInstructions(e.data) {
			s.Instructions = string(e.data)
		}
	}
	s.temp, err = os.MkdirTemp("", "jj-patch-interactive-diff-")
	if err != nil {
		return nil, err
	}
	paths := map[string]bool{}
	for p := range s.left {
		paths[p] = true
	}
	for p := range s.right {
		paths[p] = true
	}
	sorted := make([]string, 0, len(paths))
	for p := range paths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)
	for _, p := range sorted {
		a, aok := s.left[p]
		b, bok := s.right[p]
		if aok && bok && equalEntry(a, b) {
			continue
		}
		st := fileState{path: p, kind: "modified"}
		if aok {
			st.old = &a
		}
		if bok {
			st.new = &b
		}
		switch {
		case !aok:
			st.kind = "added"
		case !bok:
			st.kind = "deleted"
		case a.link != b.link:
			st.kind = "typechange"
		}
		f := File{Path: p, Kind: st.kind}
		add := func(text string, meta, mode bool, start, count int) {
			f.Hunks = append(f.Hunks, Hunk{Text: text})
			st.hunks = append(st.hunks, hunkState{text: text, metadata: meta, modeOnly: mode, start: start, count: count})
		}
		indivisible := a.link || b.link || bytes.IndexByte(a.data, 0) >= 0 || bytes.IndexByte(b.data, 0) >= 0 || (!aok && len(b.data) == 0) || (!bok && len(a.data) == 0)
		if indivisible {
			add(metadataText(st), true, false, 0, 0)
		} else {
			if !bytes.Equal(a.data, b.data) {
				texts, e := s.diff(a.data, b.data)
				if e != nil {
					return nil, fmt.Errorf("diff %q: %w", p, e)
				}
				for _, text := range texts {
					h, e := parseHunk(text, false)
					if e != nil {
						return nil, e
					}
					add(text, false, false, h.start, h.oldCount())
				}
			}
			if aok && bok && executable(a.mode) != executable(b.mode) {
				add(fmt.Sprintf("old mode %s\nnew mode %s\n", gitMode(a), gitMode(b)), true, true, 0, 0)
			}
		}
		if len(f.Hunks) == 0 {
			return nil, fmt.Errorf("cannot represent change to %q", p)
		}
		s.Files = append(s.Files, f)
		s.states = append(s.states, st)
	}
	return s, nil
}

// Close releases resources. It is idempotent and never writes a snapshot.
func (s *Session) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if s.temp != "" {
		return os.RemoveAll(s.temp)
	}
	return nil
}

// Write validates every selected hunk and the resulting path hierarchy before
// publishing. The result is always left + the selected delta, not right minus
// selections. Failed validation leaves all input/output trees untouched. Output
// changed since Open (or the previous successful Write) is rejected.
func (s *Session) Write() error {
	if s.closed {
		return fmt.Errorf("session is closed")
	}
	if err := s.checkStructure(); err != nil {
		return err
	}
	result := cloneTree(s.left)
	for i, st := range s.states {
		var ops []operation
		var selectedContent bool
		var modeSelected bool
		var wholeSelected bool
		for j, hs := range st.hunks {
			h := s.Files[i].Hunks[j]
			if h.Text != hs.text {
				return fmt.Errorf("%q hunk %d: use Edit to change hunk text", st.path, j+1)
			}
			if !h.Selected {
				continue
			}
			if hs.metadata {
				if hs.modeOnly {
					modeSelected = true
				} else {
					wholeSelected = true
				}
				continue
			}
			p, err := parseHunk(h.Text, true)
			if err != nil {
				return fmt.Errorf("%q hunk %d: %w", st.path, j+1, err)
			}
			var base []byte
			if st.old != nil {
				base = st.old.data
			}
			changes, err := p.operations(base)
			if err != nil {
				return fmt.Errorf("%q hunk %d: %w", st.path, j+1, err)
			}
			ops = append(ops, changes...)
			selectedContent = true
		}
		if wholeSelected {
			if st.new == nil {
				delete(result, st.path)
			} else {
				result[st.path] = *st.new
			}
			continue
		}
		if selectedContent {
			var base []byte
			if st.old != nil {
				base = st.old.data
			}
			data, err := applyOperations(base, ops)
			if err != nil {
				return fmt.Errorf("%q: %w", st.path, err)
			}
			if st.new == nil && len(data) == 0 {
				delete(result, st.path)
			} else if st.old != nil || len(ops) > 0 {
				e := entry{mode: 0644}
				if st.old != nil {
					e = *st.old
				} else if st.new != nil {
					e = *st.new
				}
				e.data = data
				result[st.path] = e
			}
		}
		if modeSelected {
			e, ok := result[st.path]
			if !ok {
				return fmt.Errorf("mode change without file %q", st.path)
			}
			e.mode = setExecutable(e.mode, executable(st.new.mode))
			result[st.path] = e
		}
	}
	// Preserve JJ's ignored instruction file exactly, never treating its name as
	// globally reserved. A real selected entry at that path must not be hidden.
	if _, tracked := s.left[instructionsName]; s.instructions && !tracked {
		if e, ok := s.initial[instructionsName]; ok && !e.link && IsInstructions(e.data) {
			if _, exists := result[instructionsName]; exists {
				return fmt.Errorf("selected file conflicts with synthetic %s", instructionsName)
			}
			result[instructionsName] = e
		}
	}
	if err := validateTree(result); err != nil {
		return err
	}
	if err := s.outputUnchanged(); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(s.output), ".jj-patch-interactive-publish-")
	if err != nil {
		return err
	}
	defer removeStage(stage)
	if err = writeTree(stage, result); err != nil {
		return err
	}
	if err = os.Chmod(stage, s.outputInfo.Mode().Perm()); err != nil {
		return err
	}
	if err = s.outputUnchanged(); err != nil {
		return err
	}
	if err = s.publish(stage, s.output); err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	s.initial = cloneTree(result)
	// A successful directory exchange changes the root inode.
	s.outputInfo, _ = os.Lstat(s.output)
	return nil
}
func (s *Session) outputUnchanged() error {
	if _, err := cleanRoot(s.output); err != nil {
		return err
	}
	info, err := os.Lstat(s.output)
	if err != nil {
		return err
	}
	if s.outputInfo == nil || !os.SameFile(s.outputInfo, info) {
		return fmt.Errorf("output directory was replaced since Open")
	}
	if info.Mode() != s.outputInfo.Mode() {
		return fmt.Errorf("output directory permissions changed since Open")
	}
	current, err := snapshot(s.output)
	if err != nil {
		return err
	}
	if !equalSnapshot(current, s.initial) {
		return fmt.Errorf("output changed since Open; refusing to overwrite it")
	}
	return nil
}
func (s *Session) checkStructure() error {
	if len(s.Files) != len(s.states) {
		return fmt.Errorf("session file structure was modified")
	}
	for i, st := range s.states {
		f := s.Files[i]
		if f.Path != st.path || f.Kind != st.kind || len(f.Hunks) != len(st.hunks) {
			return fmt.Errorf("session file structure was modified")
		}
	}
	return nil
}
func (s *Session) hunk(file, hunk int) (*hunkState, error) {
	if s.closed {
		return nil, fmt.Errorf("session is closed")
	}
	if err := s.checkStructure(); err != nil {
		return nil, err
	}
	if file < 0 || file >= len(s.states) || hunk < 0 || hunk >= len(s.states[file].hunks) {
		return nil, fmt.Errorf("hunk index out of range")
	}
	return &s.states[file].hunks[hunk], nil
}
func cloneTree(t tree) tree {
	r := make(tree, len(t))
	for p, e := range t {
		r[p] = e
	}
	return r
}
func equalEntry(a, b entry) bool {
	return a.link == b.link && executable(a.mode) == executable(b.mode) && bytes.Equal(a.data, b.data)
}
func equalTree(a, b tree) bool {
	if len(a) != len(b) {
		return false
	}
	for p, e := range a {
		v, ok := b[p]
		if !ok || !equalEntry(e, v) {
			return false
		}
	}
	return true
}

// Output concurrency checks also protect non-Git permission bits. Diff equality
// intentionally considers only executable mode, as jj/Git trees do.
func equalSnapshot(a, b tree) bool {
	if !equalTree(a, b) {
		return false
	}
	for p, e := range a {
		if e.mode != b[p].mode {
			return false
		}
	}
	return true
}
func executable(m os.FileMode) bool { return m&0111 != 0 }
func setExecutable(m os.FileMode, x bool) os.FileMode {
	m &^= 0111
	if x {
		m |= 0111
	}
	return m
}
func gitMode(e entry) string {
	if e.link {
		return "120000"
	}
	if executable(e.mode) {
		return "100755"
	}
	return "100644"
}
func metadataText(st fileState) string {
	var b strings.Builder
	if st.old == nil {
		fmt.Fprintf(&b, "new file mode %s\n", gitMode(*st.new))
	} else if st.new == nil {
		fmt.Fprintf(&b, "deleted file mode %s\n", gitMode(*st.old))
	} else {
		fmt.Fprintf(&b, "old mode %s\nnew mode %s\n", gitMode(*st.old), gitMode(*st.new))
	}
	if st.old != nil && st.old.link {
		fmt.Fprintf(&b, "-symlink %q\n", st.old.data)
	}
	if st.new != nil && st.new.link {
		fmt.Fprintf(&b, "+symlink %q\n", st.new.data)
	}
	if (st.old != nil && bytes.IndexByte(st.old.data, 0) >= 0) || (st.new != nil && bytes.IndexByte(st.new.data, 0) >= 0) {
		b.WriteString("Binary contents differ (indivisible)\n")
	}
	if st.kind == "typechange" {
		b.WriteString("File type and contents change together (indivisible)\n")
	}
	return b.String()
}
