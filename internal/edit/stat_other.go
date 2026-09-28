//go:build !linux && !darwin

package edit

import (
	"fmt"
	"os"
)

func readChildMode(*os.File, string) (os.FileMode, error) {
	return 0, fmt.Errorf("unsupported platform")
}
