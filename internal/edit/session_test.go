//go:build linux || darwin

package edit

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func regular(s string) entry  { return entry{data: []byte(s), mode: 0644} }
func execFile(s string) entry { return entry{data: []byte(s), mode: 0755} }
func link(s string) entry     { return entry{data: []byte(s), mode: os.ModeSymlink | 0777, link: true} }
func roots(t *testing.T, a, b tree, three bool) (string, string, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	must(t, err)
	l, r := filepath.Join(root, "left"), filepath.Join(root, "right")
	must(t, os.Mkdir(l, 0755))
	must(t, os.Mkdir(r, 0755))
	must(t, writeTree(l, a))
	must(t, writeTree(r, b))
	o := ""
	if three {
		o = filepath.Join(root, "output")
		must(t, os.Mkdir(o, 0755))
		must(t, writeTree(o, b))
	}
	return l, r, o
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func openSession(t *testing.T, l, r, o string) *Session {
	t.Helper()
	s, err := Open(l, r, o)
	must(t, err)
	t.Cleanup(func() { must(t, s.Close()) })
	return s
}
func selectAll(s *Session) {
	for i := range s.Files {
		for j := range s.Files[i].Hunks {
			s.Files[i].Hunks[j].Selected = true
		}
	}
}
func assertTree(t *testing.T, path string, want tree) {
	t.Helper()
	got, err := snapshot(path)
	must(t, err)
	if !equalTree(got, want) {
		t.Fatalf("tree mismatch at %s\ngot: %#v\nwant: %#v", path, got, want)
	}
}
func assertUntouched(t *testing.T, l, r, o string, a, b tree) {
	t.Helper()
	assertTree(t, l, a)
	assertTree(t, r, b)
	if o != "" {
		assertTree(t, o, b)
	}
}
func fileIndex(t *testing.T, s *Session, p string) int {
	t.Helper()
	for i, f := range s.Files {
		if f.Path == p {
			return i
		}
	}
	t.Fatalf("missing file %q", p)
	return -1
}
func selectPath(t *testing.T, s *Session, p string) {
	t.Helper()
	i := fileIndex(t, s, p)
	for j := range s.Files[i].Hunks {
		s.Files[i].Hunks[j].Selected = true
	}
}

func TestRoundTripMatrix(t *testing.T) {
	cases := []struct {
		name string
		a, b tree
	}{
		{"empty", tree{}, tree{}},
		{"unchanged", tree{"a": regular("same\n"), "empty": regular("")}, tree{"a": regular("same\n"), "empty": regular("")}},
		{"modify", tree{"a": regular("a\nb\nc\n")}, tree{"a": regular("a\nB\nc\n")}},
		{"added", tree{}, tree{"a": regular("new\n"), "empty": regular(""), "exe": execFile("#!/bin/sh\n"), "emptyexe": execFile("")}},
		{"deleted", tree{"a": regular("gone\n"), "empty": regular(""), "exe": execFile("x")}, tree{}},
		{"mode", tree{"a": regular("same\n")}, tree{"a": execFile("same\n")}},
		{"content-and-mode", tree{"a": regular("old\n")}, tree{"a": execFile("new\n")}},
		{"remove-executable", tree{"a": execFile("old\n")}, tree{"a": regular("new\n")}},
		{"binary", tree{"a": regular("\x00old"), "del": regular("x\x00")}, tree{"a": regular("\x00new"), "add": execFile("\xff\x00data")}},
		{"binary-to-text", tree{"a": regular("x\x00")}, tree{"a": regular("text\n")}},
		{"links", tree{"a": link("old"), "gone": link("/etc/passwd")}, tree{"a": link("../new"), "dangling": link("does-not-exist")}},
		{"type-swap", tree{"a": link("/etc/passwd"), "b": regular("text\n")}, tree{"a": execFile("plain\n"), "b": link("../../outside")}},
		{"rename", tree{"old": regular("contents\n")}, tree{"new": regular("contents\n")}},
		{"file-to-dir", tree{"a": regular("old\n")}, tree{"a/b/c": regular("new\n")}},
		{"dir-to-file", tree{"a/b/c": regular("old\n")}, tree{"a": regular("new\n")}},
		{"symlink-to-dir", tree{"a": link("../escape")}, tree{"a/b": regular("new\n")}},
		{"dir-to-symlink", tree{"a/b": regular("old\n")}, tree{"a": link("../escape")}},
		{"odd-paths", tree{"-leading": regular("before\n"), "a\nb\t\"\\": regular("before\n"), "\xff\xfe": regular("before\n")}, tree{"-leading": regular("after\n"), "a\nb\t\"\\": regular("after\n"), "\xff\xfe": regular("after\n"), ":(glob)*": regular("literal")}},
		{"missing-newline", tree{"a": regular("old"), "b": regular("old\n"), "c": regular("x")}, tree{"a": regular("new\n"), "b": regular("new"), "c": regular("y")}},
		{"crlf", tree{"a": regular("old\r\nkeep\r\n")}, tree{"a": regular("new\r\nkeep\r\n")}},
		{"long-lines", tree{"a": regular(strings.Repeat("a", 300000) + "\n")}, tree{"a": regular(strings.Repeat("b", 300000))}},
		{"non-utf8-content", tree{"a": regular("\xff\xfeold\n")}, tree{"a": regular("\xfe\xffnew\n")}},
		{"attributes", tree{".gitattributes": regular("* binary\n* filter=evil\n"), "a": regular("old\n")}, tree{".gitattributes": regular("* binary\n* filter=evil\n"), "a": regular("new\n")}},
		{"real-instructions", tree{"JJ-INSTRUCTIONS": regular("real old\n"), "d/JJ-INSTRUCTIONS": regular("nested old\n")}, tree{"JJ-INSTRUCTIONS": regular("real new\n"), "d/JJ-INSTRUCTIONS": regular("nested new\n")}},
	}
	for _, tc := range cases {
		for _, three := range []bool{false, true} {
			for _, all := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/three=%v/all=%v", tc.name, three, all), func(t *testing.T) {
					l, r, o := roots(t, tc.a, tc.b, three)
					s := openSession(t, l, r, o)
					for _, f := range s.Files {
						for _, h := range f.Hunks {
							if h.Selected {
								t.Fatal("default selection must be false")
							}
						}
					}
					assertUntouched(t, l, r, o, tc.a, tc.b)
					if all {
						selectAll(s)
					}
					must(t, s.Write())
					out := o
					if out == "" {
						out = r
					}
					want := tc.a
					if all {
						want = tc.b
					}
					assertTree(t, out, want)
					assertTree(t, l, tc.a)
					if three {
						assertTree(t, r, tc.b)
					}
					// Repeat publication must remain deterministic, not apply a delta twice.
					must(t, s.Write())
					assertTree(t, out, want)
				})
			}
		}
	}
}

func TestIndependentContentAndMode(t *testing.T) {
	for _, mask := range []int{0, 1, 2, 3} {
		t.Run(fmt.Sprint(mask), func(t *testing.T) {
			a, b := tree{"a": regular("old\n")}, tree{"a": execFile("new\n")}
			l, r, o := roots(t, a, b, true)
			s := openSession(t, l, r, o)
			if len(s.Files) != 1 || len(s.Files[0].Hunks) != 2 {
				t.Fatalf("expected text and mode hunks: %#v", s.Files)
			}
			s.Files[0].Hunks[0].Selected = mask&1 != 0
			s.Files[0].Hunks[1].Selected = mask&2 != 0
			must(t, s.Write())
			e := regular("old\n")
			if mask&1 != 0 {
				e.data = []byte("new\n")
			}
			if mask&2 != 0 {
				e.mode = 0755
			}
			assertTree(t, o, tree{"a": e})
		})
	}
}

func TestSplitSelections(t *testing.T) {
	a, b := tree{"a": regular("zero\na\ncontext\nb\nend\n")}, tree{"a": regular("zero\nA\ncontext\nB\nend\n")}
	for mask := 0; mask < 4; mask++ {
		t.Run(fmt.Sprint(mask), func(t *testing.T) {
			l, r, o := roots(t, a, b, true)
			s := openSession(t, l, r, o)
			if len(s.Files[0].Hunks) != 1 || !s.Split(0, 0) || len(s.Files[0].Hunks) != 2 {
				t.Fatal("expected a two-way split")
			}
			if s.Split(0, 0) {
				t.Fatal("first subhunk is indivisible")
			}
			for j := range s.Files[0].Hunks {
				s.Files[0].Hunks[j].Selected = mask&(1<<j) != 0
			}
			must(t, s.Write())
			x, y := "a", "b"
			if mask&1 != 0 {
				x = "A"
			}
			if mask&2 != 0 {
				y = "B"
			}
			assertTree(t, o, tree{"a": regular("zero\n" + x + "\ncontext\n" + y + "\nend\n")})
		})
	}
}

func TestDistantHunksAndLineShifts(t *testing.T) {
	old := "first\n" + strings.Repeat("middle\n", 20) + "last\n"
	new := "one\ntwo\n" + strings.Repeat("middle\n", 20) + "LAST\n"
	l, r, o := roots(t, tree{"a": regular(old)}, tree{"a": regular(new)}, true)
	s := openSession(t, l, r, o)
	if len(s.Files[0].Hunks) != 2 {
		t.Fatal("expected separate hunks")
	}
	s.Files[0].Hunks[1].Selected = true
	must(t, s.Write())
	assertTree(t, o, tree{"a": regular("first\n" + strings.Repeat("middle\n", 20) + "LAST\n")})
	s.Files[0].Hunks[0].Selected = true
	must(t, s.Write())
	assertTree(t, o, tree{"a": regular(new)})
}

func TestEditValidationAndRecount(t *testing.T) {
	a, b := tree{"a": regular("old\nkeep\n")}, tree{"a": regular("new\nkeep\n")}
	l, r, o := roots(t, a, b, true)
	s := openSession(t, l, r, o)
	original := s.Files[0].Hunks[0].Text
	for _, bad := range []string{"", "@@ -1,2 +1,2 @@\n-not-the-baseline\n+new\n keep\n", "@@ -999,2 +1,2 @@\n-old\n+new\n keep\n", "@@ -1,2 +1,2 @@\n-old\n+new\n keep\n--- /etc/passwd\n", "@@ -1,2 +1,2 @@\n-old\n+\x00\n keep\n"} {
		if err := s.Edit(0, 0, bad); err == nil {
			t.Fatalf("accepted invalid edit %q", bad)
		}
		if s.Files[0].Hunks[0].Text != original {
			t.Fatal("failed edit mutated hunk")
		}
		assertUntouched(t, l, r, o, a, b)
	}
	must(t, s.Edit(0, 0, "@@ -1,2 +1,2 @@\n old\n+extra\n+another\n keep\n"))
	s.Files[0].Hunks[0].Selected = true
	must(t, s.Write())
	assertTree(t, o, tree{"a": regular("old\nextra\nanother\nkeep\n")})
}

func TestEditNoNewlineAndCRLF(t *testing.T) {
	for _, tc := range []struct{ a, b, edit, want string }{
		{"old", "new", "@@ -1 +1 @@\n-old\n\\ No newline at end of file\n+edited\n\\ No newline at end of file\n", "edited"},
		{"old\r\n", "new\r\n", "@@ -1 +1 @@\n-old\r\n+edited\r\n", "edited\r\n"},
	} {
		l, r, o := roots(t, tree{"a": regular(tc.a)}, tree{"a": regular(tc.b)}, true)
		s := openSession(t, l, r, o)
		must(t, s.Edit(0, 0, tc.edit))
		selectAll(s)
		must(t, s.Write())
		assertTree(t, o, tree{"a": regular(tc.want)})
	}
}

func TestUnrepresentableSelectionsAreAtomic(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		a, b := tree{"a": regular("file\n")}, tree{"a/b": regular("child\n")}
		if reverse {
			a, b = b, a
		}
		l, r, o := roots(t, a, b, true)
		s := openSession(t, l, r, o)
		p := "a/b"
		if reverse {
			p = "a"
		}
		selectPath(t, s, p)
		if err := s.Write(); err == nil {
			t.Fatal("accepted file/directory collision")
		}
		assertUntouched(t, l, r, o, a, b)
		selectAll(s)
		must(t, s.Write())
		assertTree(t, o, b)
	}
}

func TestCancellationAndErrors(t *testing.T) {
	a, b := tree{"a": regular("old\n")}, tree{"a": regular("new\n")}
	l, r, o := roots(t, a, b, true)
	s := openSession(t, l, r, o)
	selectAll(s)
	s.publish = func(string, string) error { return errors.New("injected publish failure") }
	if err := s.Write(); err == nil {
		t.Fatal("missing publish failure")
	}
	assertUntouched(t, l, r, o, a, b)
	entries, err := os.ReadDir(filepath.Dir(o))
	must(t, err)
	if len(entries) != 3 {
		t.Fatalf("stage leak: %v", entries)
	}
	s.Files[0].Hunks[0].Text = "corrupt"
	if err := s.Write(); err == nil {
		t.Fatal("accepted direct text corruption")
	}
	assertUntouched(t, l, r, o, a, b)
	temp := s.temp
	must(t, s.Close())
	if _, err := os.Stat(temp); !os.IsNotExist(err) {
		t.Fatal("temporary diff directory not cleaned")
	}
	must(t, s.Close())
	if err := s.Write(); err == nil {
		t.Fatal("write after close")
	}
	if s.Split(0, 0) {
		t.Fatal("split after close")
	}
	if s.Edit(0, 0, "") == nil {
		t.Fatal("edit after close")
	}
	assertUntouched(t, l, r, o, a, b)
}

func TestOutputConcurrentChangeRejected(t *testing.T) {
	a, b := tree{"a": regular("old\n")}, tree{"a": regular("new\n")}
	l, r, o := roots(t, a, b, true)
	s := openSession(t, l, r, o)
	selectAll(s)
	must(t, os.WriteFile(filepath.Join(o, "a"), []byte("external\n"), 0644))
	if err := s.Write(); err == nil {
		t.Fatal("overwrote concurrent edit")
	}
	assertTree(t, o, tree{"a": regular("external\n")})
}

func TestNoSymlinkTraversal(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret")
	must(t, os.WriteFile(secret, []byte("untouched"), 0600))
	a, b := tree{"escape": link(outside), "dangling": link("/no/such/path")}, tree{"escape": link("/etc/passwd"), "dangling": link(outside)}
	l, r, o := roots(t, a, b, true)
	s := openSession(t, l, r, o)
	selectAll(s)
	must(t, s.Write())
	assertTree(t, o, b)
	data, err := os.ReadFile(secret)
	must(t, err)
	if string(data) != "untouched" {
		t.Fatal("followed symlink")
	}
	alias := filepath.Join(filepath.Dir(l), "alias")
	must(t, os.Symlink(l, alias))
	parentAlias := filepath.Join(t.TempDir(), "alias")
	must(t, os.Symlink(filepath.Dir(l), parentAlias))
	for _, argument := range []string{alias, filepath.Join(parentAlias, "left")} {
		aliased, err := Open(argument, r, o)
		if runtime.GOOS == "darwin" {
			must(t, err)
			must(t, aliased.Close())
		} else if err == nil {
			aliased.Close()
			t.Fatal("accepted symlink root/ancestor")
		}
	}
	saved := o + "-saved"
	must(t, os.Rename(o, saved))
	must(t, os.Symlink(outside, o))
	if err := s.Write(); err == nil {
		t.Fatal("accepted replaced output root")
	}
	data, err = os.ReadFile(secret)
	must(t, err)
	if string(data) != "untouched" {
		t.Fatal("wrote through output link")
	}
}

func TestRejectUnsupportedAndOverlappingRoots(t *testing.T) {
	l, r, o := roots(t, tree{}, tree{}, true)
	must(t, syscall.Mkfifo(filepath.Join(l, "fifo"), 0600))
	if _, err := Open(l, r, o); err == nil {
		t.Fatal("accepted fifo")
	}
	must(t, os.Remove(filepath.Join(l, "fifo")))
	for _, args := range [][3]string{{l, l, o}, {l, r, l}, {l, r, filepath.Dir(l)}, {filepath.Dir(l), r, o}, {"", r, o}} {
		if s, err := Open(args[0], args[1], args[2]); err == nil {
			s.Close()
			t.Fatalf("accepted bad roots %v", args)
		}
	}
}

func TestGitIsolation(t *testing.T) {
	home := t.TempDir()
	marker := filepath.Join(home, "EXECUTED")
	config := filepath.Join(home, ".gitconfig")
	must(t, os.WriteFile(config, []byte("[diff]\n external = sh -c 'touch "+marker+"'\n algorithm = invalid\n[core]\n autocrlf = true\n attributesFile = "+filepath.Join(home, "attributes")+"\n[filter \"evil\"]\n clean = sh -c 'touch "+marker+"'\n required = true\n"), 0600))
	must(t, os.WriteFile(filepath.Join(home, "attributes"), []byte("* binary filter=evil\n"), 0600))
	fakeIndex := filepath.Join(home, "index")
	must(t, os.WriteFile(fakeIndex, []byte("caller index untouched"), 0600))
	for k, v := range map[string]string{"HOME": home, "XDG_CONFIG_HOME": home, "GIT_CONFIG_GLOBAL": config, "GIT_CONFIG_SYSTEM": config, "GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "diff.algorithm", "GIT_CONFIG_VALUE_0": "invalid", "GIT_DIR": home, "GIT_WORK_TREE": home, "GIT_INDEX_FILE": fakeIndex, "GIT_EXTERNAL_DIFF": "false", "GIT_DIFF_OPTS": "--unified=0", "GIT_CONFIG_PARAMETERS": "garbage", "GIT_OBJECT_DIRECTORY": "/no/such/path", "GIT_ALTERNATE_OBJECT_DIRECTORIES": "/no/such/path"} {
		t.Setenv(k, v)
	}
	a, b := tree{"a": regular("old\r\n")}, tree{"a": regular("new\r\n")}
	l, r, o := roots(t, a, b, true)
	s := openSession(t, l, r, o)
	selectAll(s)
	must(t, s.Write())
	assertTree(t, o, b)
	data, err := os.ReadFile(fakeIndex)
	must(t, err)
	if string(data) != "caller index untouched" {
		t.Fatal("caller index changed")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("external command executed")
	}
}

const synthetic = "You are editing changes in: abc description\n\nThe diff initially shows the commit's changes.\n\nAdjust the right side until it shows the contents you want. If you\ndon't make any changes, then the operation will be aborted."

func TestInstructions(t *testing.T) {
	for _, three := range []bool{false, true} {
		a, b := tree{"a": regular("old\n")}, tree{"a": regular("new\n"), "JJ-INSTRUCTIONS": regular(synthetic), "d/JJ-INSTRUCTIONS": regular(synthetic)}
		l, r, o := roots(t, a, b, three)
		s := openSession(t, l, r, o)
		if s.Instructions != synthetic {
			t.Fatal("instructions not recognized")
		}
		for _, f := range s.Files {
			if f.Path == "JJ-INSTRUCTIONS" {
				t.Fatal("synthetic instructions became selectable")
			}
		}
		if len(s.Files) != 2 {
			t.Fatal("nested instructions were excluded")
		}
		must(t, s.Write())
		out := o
		if out == "" {
			out = r
		}
		assertTree(t, out, tree{"a": regular("old\n"), "JJ-INSTRUCTIONS": regular(synthetic)})
	}
	// A real tracked file remains selectable, even if its content looks synthetic.
	a, b := tree{"JJ-INSTRUCTIONS": regular("tracked\n")}, tree{"JJ-INSTRUCTIONS": regular(synthetic)}
	l, r, o := roots(t, a, b, true)
	s := openSession(t, l, r, o)
	if s.Instructions != "" || len(s.Files) != 1 {
		t.Fatal("lost real instruction file")
	}
	selectAll(s)
	must(t, s.Write())
	assertTree(t, o, b)
	// An ordinary new file with the reserved-looking name is still an addition.
	l, r, o = roots(t, tree{}, tree{"JJ-INSTRUCTIONS": regular("user contents\n")}, true)
	s = openSession(t, l, r, o)
	if len(s.Files) != 1 {
		t.Fatal("lost added real instruction file")
	}
	selectAll(s)
	must(t, s.Write())
	assertTree(t, o, tree{"JJ-INSTRUCTIONS": regular("user contents\n")})
	// Instructions symlinks must not be read or excluded.
	l, r, o = roots(t, tree{}, tree{"JJ-INSTRUCTIONS": link("/etc/passwd")}, true)
	s = openSession(t, l, r, o)
	if s.Instructions != "" || len(s.Files) != 1 {
		t.Fatal("followed/excluded instructions link")
	}
}

func TestMetadataIsIndivisible(t *testing.T) {
	l, r, o := roots(t, tree{"binary": regular("\x00old"), "mode": regular("same")}, tree{"binary": regular("\x00new"), "mode": execFile("same"), "empty": regular(""), "link": link("target")}, true)
	s := openSession(t, l, r, o)
	for i, f := range s.Files {
		if len(f.Hunks) != 1 || s.Split(i, 0) {
			t.Fatalf("split metadata for %s", f.Path)
		}
		if s.Edit(i, 0, "arbitrary") == nil {
			t.Fatalf("edited metadata for %s", f.Path)
		}
	}
}

func TestRandomRoundTripSplitAndSubset(t *testing.T) {
	rng := rand.New(rand.NewSource(721))
	for iteration := 0; iteration < 80; iteration++ {
		var old, new strings.Builder
		n := 2 + rng.Intn(30)
		for i := 0; i < n; i++ {
			text := fmt.Sprintf("line %d\n", i)
			old.WriteString(text)
			switch rng.Intn(5) {
			case 0:
				new.WriteString(fmt.Sprintf("REPLACEMENT %d\n", i))
			case 1:
			case 2:
				new.WriteString("INSERT\n")
				new.WriteString(text)
			default:
				new.WriteString(text)
			}
		}
		l, r, o := roots(t, tree{"a": regular(old.String())}, tree{"a": regular(new.String())}, true)
		s := openSession(t, l, r, o)
		for i := range s.Files {
			for j := 0; j < len(s.Files[i].Hunks); j++ {
				for s.Split(i, j) {
				}
			}
		}
		selectAll(s)
		must(t, s.Write())
		assertTree(t, o, tree{"a": regular(new.String())})
		// A random subset must either apply cleanly or expose a genuine representational
		// conflict, never silently consume overlapping context. Text-only splits have
		// no conflicts here because every old/new line is newline-terminated.
		for i := range s.Files {
			for j := range s.Files[i].Hunks {
				s.Files[i].Hunks[j].Selected = rng.Intn(2) == 0
			}
		}
		must(t, s.Write())
		must(t, s.Close())
	}
}

func TestInvalidSelectionBeforeAnyMutation(t *testing.T) {
	a, b := tree{"a": regular("old\n"), "z": regular("old\n")}, tree{"a": regular("new\n"), "z": regular("new\n")}
	l, r, o := roots(t, a, b, true)
	s := openSession(t, l, r, o)
	selectAll(s)
	s.Files[1].Path = "../../escape"
	if err := s.Write(); err == nil {
		t.Fatal("accepted path tampering")
	}
	assertUntouched(t, l, r, o, a, b)
	s.Files[1].Path = "z"
	s.Files[1].Hunks[0].Text = "invalid"
	if err := s.Write(); err == nil {
		t.Fatal("accepted invalid final hunk")
	}
	assertUntouched(t, l, r, o, a, b)
}

func TestPartialDeletionAndAdditionEdit(t *testing.T) {
	l, r, o := roots(t, tree{"gone": regular("one\ntwo\n")}, tree{"added": regular("one\ntwo\n")}, true)
	s := openSession(t, l, r, o)
	i := fileIndex(t, s, "gone")
	must(t, s.Edit(i, 0, "@@ -1,2 +0,0 @@\n-one\n two\n"))
	s.Files[i].Hunks[0].Selected = true
	i = fileIndex(t, s, "added")
	must(t, s.Edit(i, 0, "@@ -0,0 +1,2 @@\n+one\n"))
	s.Files[i].Hunks[0].Selected = true
	must(t, s.Write())
	assertTree(t, o, tree{"gone": regular("two\n"), "added": regular("one\n")})
}

func TestParseLongLineAndMaliciousHeader(t *testing.T) {
	text := "@@ -1 +1 @@\n-" + strings.Repeat("x", 1<<20) + "\n+new\n"
	p, err := parseHunk(text, true)
	must(t, err)
	if len(p.lines) != 2 {
		t.Fatal("long line truncated")
	}
	for _, bad := range []string{"@@ -999999999999999999999999999999 +1 @@\n-a\n+b\n", "@@ -0,1 +1 @@\n-a\n+b\n", "@@ -1 +1 @@\n\\ No newline at end of file\n-a\n+b\n", "@@ -1 +1 @@\n-a\n+b\n\\ No newline at end of file\n\\ No newline at end of file\n"} {
		if _, err := parseHunk(bad, true); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestSnapshotsOwnBytes(t *testing.T) {
	l, r, o := roots(t, tree{"a": regular("old\n")}, tree{"a": regular("new\n")}, true)
	s := openSession(t, l, r, o)
	must(t, os.WriteFile(filepath.Join(l, "a"), []byte("changed after open"), 0644))
	selectAll(s)
	must(t, s.Write())
	got, err := os.ReadFile(filepath.Join(o, "a"))
	must(t, err)
	if !bytes.Equal(got, []byte("new\n")) {
		t.Fatal("baseline was not captured")
	}
}

func TestCloneIsolatesSplitsEditsAndSelections(t *testing.T) {
	a, b := tree{"a": regular("a\nold\nmiddle\nold2\nz\n")}, tree{"a": regular("a\nnew\nmiddle\nnew2\nz\n")}
	l, r, o := roots(t, a, b, true)
	s := openSession(t, l, r, o)
	original := s.Files[0].Hunks[0].Text
	work := s.Clone()
	if !work.Split(0, 0) {
		t.Fatal("split failed")
	}
	work.Files[0].Hunks[0].Selected = true
	text := strings.Replace(work.Files[0].Hunks[0].Text, "+new\n", "+edited\n", 1)
	must(t, work.Edit(0, 0, text))
	if len(s.Files[0].Hunks) != 1 || s.Files[0].Hunks[0].Text != original || s.Files[0].Hunks[0].Selected {
		t.Fatal("clone modified public source")
	}
	must(t, s.checkStructure())
	must(t, s.Write())
	assertTree(t, o, a)
	// Adopt a new working copy exactly as the prompt does, then publish it.
	work = s.Clone()
	if !work.Split(0, 0) {
		t.Fatal("source's private state was corrupted")
	}
	work.Files[0].Hunks[0].Selected = true
	*s = *work
	must(t, s.Write())
	assertTree(t, o, tree{"a": regular("a\nnew\nmiddle\nold2\nz\n")})
}

func TestAllKnownInstructionPreambles(t *testing.T) {
	const three = "You are using the experimental 3-pane diff editor config. Some of\nthe following instructions may have been written with a 2-pane\ndiff editing in mind and be a little inaccurate.\n\n"
	instructions := []string{
		synthetic,
		"You are moving changes from: abc\ninto commit: def\n\nAdjust the right side until the diff shows the changes you want to move\nto the destination.",
		"You are splitting a commit into two: abc\n\nThe diff initially shows the changes in the commit you're splitting.\n",
		"You are restoring changes from: abc\nto commit: def\n\nThe diff initially shows all changes restored.\n",
		"You are splitting the working-copy commit: abc\n\nThe diff initially shows all changes. Adjust the right side until it shows the\ncontents you want for the first commit. The remainder will be included in the\nnew working-copy commit.\n",
		"You are selecting changes from: abc to be considered for\nabsorption into ancestors.\n\nThe left side of the diff shows the parent commit.\n\nAdjust the right side until the diff shows the changes you want to\nabsorb. Selected hunks will be considered for assignment to the\nclosest ancestor.\n",
		"Please make your edits in this pane.\n\n" + three + synthetic,
		"The content of this pane should NOT be edited. Any edits will be\nlost.\n\n" + three + synthetic,
	}
	for i, text := range instructions {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			if !IsInstructions([]byte(text)) {
				t.Fatal("not recognized")
			}
			l, r, o := roots(t, tree{}, tree{"JJ-INSTRUCTIONS": regular(text)}, false)
			s := openSession(t, l, r, o)
			if len(s.Files) != 0 || s.Instructions != text {
				t.Fatal("instructions leaked into diff")
			}
			must(t, s.Write())
			assertTree(t, r, tree{"JJ-INSTRUCTIONS": regular(text)})
		})
	}
	for _, text := range []string{"JJ-INSTRUCTIONS", "You are editing changes in: a real document", "Please make your edits in this pane.\n\nordinary content"} {
		if IsInstructions([]byte(text)) {
			t.Fatal("over-broad instruction recognition")
		}
	}
}

func TestThreeDirectoryDistinctInstructions(t *testing.T) {
	const three = "You are using the experimental 3-pane diff editor config. Some of\nthe following instructions may have been written with a 2-pane\ndiff editing in mind and be a little inaccurate.\n\n"
	right := regular("The content of this pane should NOT be edited. Any edits will be\nlost.\n\n" + three + synthetic)
	output := regular("Please make your edits in this pane.\n\n" + three + synthetic)
	l, r, o := roots(t, tree{}, tree{"JJ-INSTRUCTIONS": right, "a": regular("new\n")}, true)
	must(t, os.WriteFile(filepath.Join(o, "JJ-INSTRUCTIONS"), output.data, 0644))
	s := openSession(t, l, r, o)
	if s.Instructions != string(output.data) || len(s.Files) != 1 || s.Files[0].Path != "a" {
		t.Fatal("wrong instructions chosen")
	}
	must(t, s.Write())
	assertTree(t, o, tree{"JJ-INSTRUCTIONS": output})
	assertTree(t, r, tree{"JJ-INSTRUCTIONS": right, "a": regular("new\n")})
}

func TestOutputPermissionChangesRejected(t *testing.T) {
	for _, rootChange := range []bool{false, true} {
		l, r, o := roots(t, tree{"a": regular("old\n")}, tree{"a": regular("new\n")}, true)
		s := openSession(t, l, r, o)
		name := filepath.Join(o, "a")
		mode := os.FileMode(0600)
		if rootChange {
			name = o
			mode = 0700
		}
		must(t, os.Chmod(name, mode))
		if err := s.Write(); err == nil {
			t.Fatal("overwrote changed permissions")
		}
	}
}

func TestGitFailureDoesNotChangeInputs(t *testing.T) {
	a, b := tree{"a": regular("old\n")}, tree{"a": regular("new\n")}
	l, r, o := roots(t, a, b, true)
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	t.Setenv("PATH", temp)
	if s, err := Open(l, r, o); err == nil {
		s.Close()
		t.Fatal("accepted missing git")
	}
	assertUntouched(t, l, r, o, a, b)
	entries, err := os.ReadDir(temp)
	must(t, err)
	if len(entries) > 0 {
		t.Fatalf("Open failure leaked resources: %v", entries)
	}
}

func TestSplitSelectedAndInvalidIndices(t *testing.T) {
	l, r, o := roots(t, tree{"a": regular("old\ncontext\nold2")}, tree{"a": regular("new\ncontext\nnew2")}, true)
	s := openSession(t, l, r, o)
	selectAll(s)
	if !s.Split(0, 0) {
		t.Fatal("cannot split no-newline diff")
	}
	for _, h := range s.Files[0].Hunks {
		if !h.Selected {
			t.Fatal("split lost selection")
		}
	}
	must(t, s.Write())
	assertTree(t, o, tree{"a": regular("new\ncontext\nnew2")})
	for _, indices := range [][2]int{{-1, 0}, {0, -1}, {9, 0}, {0, 9}} {
		if s.Split(indices[0], indices[1]) {
			t.Fatal("split invalid index")
		}
		if s.Edit(indices[0], indices[1], "") == nil {
			t.Fatal("edit invalid index")
		}
	}
}

func TestNoNewlineInjectionRejected(t *testing.T) {
	l, r, o := roots(t, tree{"a": regular("old\nkeep\n")}, tree{"a": regular("new\nkeep\n")}, true)
	s := openSession(t, l, r, o)
	text := "@@ -1,2 +1,2 @@\n-old\n+no newline\n\\ No newline at end of file\n keep\n"
	if err := s.Edit(0, 0, text); err == nil {
		t.Fatal("accepted data after unterminated line")
	}
	assertTree(t, o, tree{"a": regular("new\nkeep\n")})
}

func TestReadOnlyOutputCleanupAndHardlinkIsolation(t *testing.T) {
	a, b := tree{"d/a": regular("old\n")}, tree{"d/a": regular("new\n")}
	l, r, o := roots(t, a, b, true)
	// A hard-linked output inode must never be edited in place.
	outside := filepath.Join(t.TempDir(), "external")
	must(t, os.Link(filepath.Join(o, "d/a"), outside))
	must(t, os.Chmod(filepath.Join(o, "d"), 0555))
	must(t, os.Chmod(o, 0555))
	s := openSession(t, l, r, o)
	must(t, s.Write())
	assertTree(t, o, a)
	got, err := os.ReadFile(outside)
	must(t, err)
	if string(got) != "new\n" {
		t.Fatal("modified a hard-linked outside inode")
	}
	entries, err := os.ReadDir(filepath.Dir(o))
	must(t, err)
	if len(entries) != 3 {
		t.Fatalf("read-only old tree leaked: %v", entries)
	}
	must(t, os.Chmod(o, 0755))
}

func TestEditDiscardAllAdditionsIsNoOp(t *testing.T) {
	for _, three := range []bool{false, true} {
		l, r, o := roots(t, tree{"existing": regular("old\n")}, tree{"existing": regular("new\n"), "added": regular("one\ntwo\n")}, three)
		s := openSession(t, l, r, o)
		i := fileIndex(t, s, "added")
		original := s.Files[i].Hunks[0].Text
		header := strings.SplitAfter(original, "\n")[0]
		must(t, s.Edit(i, 0, header))
		s.Files[i].Hunks[0].Selected = true
		if _, err := parseHunk(s.Files[i].Hunks[0].Text, true); err != nil {
			t.Fatalf("normalized zero/zero hunk invalid: %v", err)
		}
		i = fileIndex(t, s, "existing")
		must(t, s.Edit(i, 0, "@@ -1 +1 @@\n old\n"))
		s.Files[i].Hunks[0].Selected = true
		must(t, s.Write())
		out := o
		if out == "" {
			out = r
		}
		assertTree(t, out, tree{"existing": regular("old\n")})
	}
}

func TestInstructionDetectionOptOut(t *testing.T) {
	for _, three := range []bool{false, true} {
		for _, selected := range []bool{false, true} {
			real := tree{"JJ-INSTRUCTIONS": regular(synthetic)}
			l, r, o := roots(t, tree{}, real, three)
			s, err := OpenWithOptions(l, r, o, Options{Instructions: false})
			must(t, err)
			t.Cleanup(func() { must(t, s.Close()) })
			if s.Instructions != "" || len(s.Files) != 1 || s.Files[0].Path != "JJ-INSTRUCTIONS" {
				t.Fatal("opt-out hid real matching instruction file")
			}
			if selected {
				selectAll(s)
			}
			must(t, s.Write())
			out := o
			if out == "" {
				out = r
			}
			want := tree{}
			if selected {
				want = real
			}
			assertTree(t, out, want)
		}
	}
}
