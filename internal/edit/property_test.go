//go:build linux || darwin

package edit

import (
	"bytes"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

// Independent baseline-coordinate oracle: remove marked old lines and attach
// additions to old-line boundaries, then walk the baseline exactly once. It
// intentionally does not use parsedHunk.operations or applyOperations.
func referenceText(t *testing.T, base []byte, hunks []Hunk) []byte {
	t.Helper()
	original := strings.SplitAfter(string(base), "\n")
	if original[len(original)-1] == "" {
		original = original[:len(original)-1]
	}
	removed := map[int]bool{}
	inserted := map[int][]string{}
	for _, h := range hunks {
		if !h.Selected || !strings.HasPrefix(h.Text, "@@ ") {
			continue
		}
		raw := strings.SplitAfter(h.Text, "\n")
		fields := strings.Fields(raw[0])
		coords := strings.Split(strings.TrimPrefix(fields[1], "-"), ",")
		position, err := strconv.Atoi(coords[0])
		must(t, err)
		if len(coords) < 2 || coords[1] != "0" {
			position--
		}
		lastAddition := -1
		for _, line := range raw[1 : len(raw)-1] {
			switch line[0] {
			case ' ':
				position++
				lastAddition = -1
			case '-':
				removed[position] = true
				position++
				lastAddition = -1
			case '+':
				inserted[position] = append(inserted[position], line[1:])
				lastAddition = position
			case '\\':
				if lastAddition >= 0 {
					v := inserted[lastAddition]
					v[len(v)-1] = strings.TrimSuffix(v[len(v)-1], "\n")
					inserted[lastAddition] = v
				}
			}
		}
	}
	var out strings.Builder
	for i := 0; i <= len(original); i++ {
		for _, line := range inserted[i] {
			out.WriteString(line)
		}
		if i < len(original) && !removed[i] {
			out.WriteString(original[i])
		}
	}
	return []byte(out.String())
}

func TestPropertyArbitraryEditsAndSplitSubsets(t *testing.T) {
	rng := rand.New(rand.NewSource(0x51ec7))
	randomBinary := func() entry {
		data := make([]byte, 1+rng.Intn(100))
		for i := range data {
			data[i] = byte(rng.Intn(256))
		}
		data[rng.Intn(len(data))] = 0
		e := regular(string(data))
		if rng.Intn(2) == 0 {
			e.mode = 0755
		}
		return e
	}
	for iteration := 0; iteration < 100; iteration++ {
		t.Run(fmt.Sprint(iteration), func(t *testing.T) {
			n := rng.Intn(32)
			var old, new strings.Builder
			for i := 0; i < n; i++ {
				text := fmt.Sprintf("line %d: %c\n", i, byte(32+rng.Intn(90)))
				old.WriteString(text)
				for k := rng.Intn(3); k > 0; k-- {
					fmt.Fprintf(&new, "insert before %d/%d\n", i, k)
				}
				switch rng.Intn(4) {
				case 0:
				case 1:
					fmt.Fprintf(&new, "replace %d\n", i)
				default:
					new.WriteString(text)
				}
			}
			if rng.Intn(2) == 0 {
				new.WriteString("appended\n")
			}
			oldText, newText := old.String(), new.String()
			if rng.Intn(2) == 0 {
				oldText = strings.TrimSuffix(oldText, "\n")
			}
			if rng.Intn(2) == 0 {
				newText = strings.TrimSuffix(newText, "\n")
			}
			a, b := tree{"text": regular(oldText), "empty-delete": regular(""), "binary": randomBinary()}, tree{"text": regular(newText), "empty-add": regular(""), "binary": randomBinary()}
			if rng.Intn(2) == 0 {
				a["text"] = execFile(oldText)
			}
			if rng.Intn(2) == 0 {
				b["text"] = execFile(newText)
			}
			if rng.Intn(2) == 0 {
				a["empty-delete"] = execFile("")
			}
			if rng.Intn(2) == 0 {
				b["empty-add"] = execFile("")
			}
			l, r, o := roots(t, a, b, true)
			s := openSession(t, l, r, o)
			must(t, s.Write())
			assertTree(t, o, a)
			selectAll(s)
			must(t, s.Write())
			assertTree(t, o, b)
			for i := range s.Files {
				for j := 0; j < len(s.Files[i].Hunks); j++ {
					for s.Split(i, j) {
					}
				}
			}
			must(t, s.Write())
			assertTree(t, o, b)
			want := cloneTree(a)
			for i, f := range s.Files {
				for j := range s.Files[i].Hunks {
					s.Files[i].Hunks[j].Selected = rng.Intn(2) == 0
				}
				if f.Path == "text" {
					e := a["text"]
					e.data = referenceText(t, e.data, s.Files[i].Hunks)
					for _, h := range s.Files[i].Hunks {
						if h.Selected && strings.HasPrefix(h.Text, "old mode ") {
							e.mode = b["text"].mode
						}
					}
					want["text"] = e
				} else if s.Files[i].Hunks[0].Selected {
					if e, exists := b[f.Path]; exists {
						want[f.Path] = e
					} else {
						delete(want, f.Path)
					}
				}
			}
			must(t, s.Write())
			assertTree(t, o, want)
			// Replay must not compound insertion offsets or apply selected hunks twice.
			must(t, s.Write())
			assertTree(t, o, want)
		})
	}
}

func FuzzHunkParserRoundTrip(f *testing.F) {
	for _, seed := range []string{"@@ -1 +1 @@\n-old\n+new\n", "@@ -0,0 +0,0 @@\n", "@@ -1 +1 @@\n-x\n\\ No newline at end of file\n+y\n\\ No newline at end of file\n", "@@ -1 +1 @@\n-a\r\n+b\r\n"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		p, err := parseHunk(text, true)
		if err != nil {
			return
		}
		q, err := parseHunk(p.render(), true)
		if err != nil {
			t.Fatalf("rendered parsed hunk is invalid: %v", err)
		}
		if q.start != p.start || q.newStart != p.newStart || len(q.lines) != len(p.lines) {
			t.Fatal("coordinate roundtrip changed")
		}
		for i := range p.lines {
			if p.lines[i].kind != q.lines[i].kind || !bytes.Equal([]byte(p.lines[i].data), []byte(q.lines[i].data)) {
				t.Fatal("line roundtrip changed")
			}
		}
	})
}
