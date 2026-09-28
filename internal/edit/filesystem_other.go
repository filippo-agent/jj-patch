//go:build !linux && !darwin

package edit

import (
	"fmt"
	"os"
)

// Fail closed on platforms where a descriptor-relative no-follow walker and
// atomic directory exchange have not yet been implemented. Do not replace this
// with recursive copies or rename-away/rename-back publication: those lose the
// session's atomic-error and no-symlink-traversal guarantees.
func openDirectory(string) (*os.File, error) {
	return nil, fmt.Errorf("secure snapshot editing currently requires Linux or macOS")
}
func openChildDirectory(*os.File, string) (*os.File, error) {
	return nil, fmt.Errorf("unsupported platform")
}
func openChildFile(*os.File, string) (*os.File, error) {
	return nil, fmt.Errorf("unsupported platform")
}
func readChildLink(*os.File, string) (string, error) { return "", fmt.Errorf("unsupported platform") }
func publishDirectory(string, string) error {
	return fmt.Errorf("atomic directory publication is unsupported on this platform")
}
