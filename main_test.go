package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContext(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"You are splitting a commit into two: abc", "in the first change"},
		{"You are splitting the working-copy commit: abc", "in this commit"},
		{"Please make your edits in this pane.\n\nYou are using the experimental 3-pane diff editor config. Some of\nthe following instructions may have been written with a 2-pane\ndiff editing in mind and be a little inaccurate.\n\nYou are editing changes in: abc", "in the edited change"},
		{"You are moving changes from: abc", "to move to the destination"},
		{"You are restoring changes from: abc", "to restore"},
		{"You are selecting changes from: abc to be considered for\nabsorption into ancestors.", "for absorption into ancestors"},
		{"unknown future instruction", "in the result"},
		{"You are editing changes in: abc You are splitting a commit into two:", "in the edited change"},
		{"unknown\nYou are splitting a commit into two:", "in the result"},
		{"", "in the result"},
	} {
		got, err := promptContext("auto", tc.input)
		if err != nil || got != tc.want {
			t.Errorf("%q: %q, %v", tc.input, got, err)
		}
	}
	if got, _ := promptContext("split", "You are editing changes in:"); got != "in the first change" {
		t.Fatal(got)
	}
	if got, _ := promptContext("commit", "You are splitting a commit into two: abc"); got != "in this commit" {
		t.Fatal(got)
	}
	if _, err := promptContext("oops", ""); err == nil {
		t.Fatal("accepted invalid context")
	}
}

func TestUsage(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"--version"}} {
		var out bytes.Buffer
		if err := run(args, strings.NewReader(""), &out, &out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "jj-patch-interactive") {
			t.Fatal(out.String())
		}
	}
	for _, args := range [][]string{nil, {"--patch"}, {"a"}, {"a", "b", "c"}, {"--context", "oops", "a", "b"}} {
		if err := run(args, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestInstructionOptOut(t *testing.T) {
	root := t.TempDir()
	left, right := filepath.Join(root, "left"), filepath.Join(root, "right")
	for _, path := range []string{left, right} {
		if err := os.Mkdir(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	content := "You are editing changes in: a real documentation example\n\nAdjust the right side until it shows the contents you want.\n"
	path := filepath.Join(right, "JJ-INSTRUCTIONS")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"--no-instructions", left, right}, strings.NewReader("n\n"), &out, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"JJ-INSTRUCTIONS"`) || !strings.Contains(out.String(), "in the result?") {
		t.Fatalf("real instruction-like file was hidden: %s", out.String())
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("rejected addition still exists: %v", err)
	}
}

func TestFlagDiagnosticsCannotEmitControls(t *testing.T) {
	for _, arg := range []string{"--bad\x1b[2J", "--bad\u034f\ufe0f\u2800", "--bad\xff"} {
		var out bytes.Buffer
		if err := run([]string{arg}, strings.NewReader(""), &out, &out); err == nil {
			t.Fatal("invalid flag accepted")
		}
		got := out.String()
		for _, raw := range []string{"\x1b", "\u034f", "\ufe0f", "\u2800", "\xff"} {
			if strings.Contains(got, raw) {
				t.Fatalf("flag diagnostics emitted raw unsafe bytes: %q", got)
			}
		}
		if !strings.Contains(got, "Usage: jj-patch-interactive") {
			t.Fatal("lost flag help")
		}
	}
}
