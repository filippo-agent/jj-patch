package prompt

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

type terminalCapture struct {
	bytes.Buffer
	fd uintptr
}

func (w *terminalCapture) Fd() uintptr { return w.fd }

func TestTerminalColorAndOptOut(t *testing.T) {
	pty, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("PTY unavailable: %v", err)
	}
	defer pty.Close()
	if !isTerminal(pty.Fd()) {
		t.Fatal("PTY not detected as terminal")
	}
	for _, tc := range []struct {
		name, term, noColor string
		unsetNoColor, want  bool
	}{
		{"terminal", "xterm", "", true, true},
		{"NO_COLOR", "xterm", "1", false, false},
		{"empty NO_COLOR", "xterm", "", false, false},
		{"dumb terminal", "dumb", "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TERM", tc.term)
			t.Setenv("NO_COLOR", tc.noColor)
			if tc.unsetNoColor {
				os.Unsetenv("NO_COLOR")
			}
			out := &terminalCapture{fd: pty.Fd()}
			if err := Run(sample(), strings.NewReader("q\n"), out, "in the first change"); err != nil {
				t.Fatal(err)
			}
			got := strings.Contains(out.String(), "\x1b[")
			if got != tc.want {
				t.Fatalf("ANSI color = %v, want %v: %q", got, tc.want, out.String())
			}
		})
	}
}
