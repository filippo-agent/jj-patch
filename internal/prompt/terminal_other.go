//go:build !linux && !darwin

package prompt

// Other platforms retain the complete line UI without assuming ANSI support.
func isTerminal(uintptr) bool { return false }
