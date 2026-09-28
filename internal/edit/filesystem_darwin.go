//go:build darwin

package edit

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/sys/unix"
)

// Roots must already be absolute and canonicalized by cleanRoot. In particular,
// resolving macOS's /var alias belongs at the argument boundary, not here:
// subsequent snapshot walks must still reject replaced symlink ancestors.
func openDirectory(path string) (*os.File, error) {
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	current := "/"
	for _, part := range strings.Split(filepath.Clean(path), "/") {
		if part == "" {
			continue
		}
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if err != nil {
			return nil, err
		}
		fd = next
		current = filepath.Join(current, part)
	}
	return os.NewFile(uintptr(fd), current), nil
}

func openChildDirectory(dir *os.File, name string) (*os.File, error) {
	return openChild(dir, name, unix.O_DIRECTORY)
}

func openChildFile(dir *os.File, name string) (*os.File, error) {
	return openChild(dir, name, unix.O_NONBLOCK)
}

// name is a single directory entry from ReadDir, never a relative path with
// intermediate components (O_NOFOLLOW applies only to the final component).
func openChild(dir *os.File, name string, flags int) (*os.File, error) {
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|flags, 0)
	runtime.KeepAlive(dir)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), filepath.Join(dir.Name(), name)), nil
}

func readChildLink(dir *os.File, name string) (string, error) {
	for size := 256; size <= 1<<20; size *= 2 {
		b := make([]byte, size)
		n, err := unix.Readlinkat(int(dir.Fd()), name, b)
		runtime.KeepAlive(dir)
		if err != nil {
			return "", err
		}
		if n < len(b) {
			return string(b[:n]), nil
		}
	}
	return "", fmt.Errorf("symlink target too long")
}

// RENAME_SWAP atomically exchanges two existing entries on a supporting
// filesystem, including nonempty directories. The previous output is left at
// stage for the caller to remove after success. Unsupported filesystems fail
// closed: there is no rename-away/rename-back or per-file publication fallback.
func publishDirectory(stage, output string) error {
	if filepath.Dir(stage) != filepath.Dir(output) {
		return fmt.Errorf("stage and output must share a parent")
	}
	parent, err := openDirectory(filepath.Dir(output))
	if err != nil {
		return err
	}
	defer parent.Close()
	err = unix.RenameatxNp(int(parent.Fd()), filepath.Base(stage), int(parent.Fd()), filepath.Base(output), unix.RENAME_SWAP)
	runtime.KeepAlive(parent)
	if err != nil {
		return fmt.Errorf("atomic directory exchange: %w", err)
	}
	return nil
}
