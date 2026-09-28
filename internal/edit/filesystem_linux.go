//go:build linux

package edit

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/sys/unix"
)

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
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return nil, e
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

// RENAME_EXCHANGE is an atomic same-filesystem directory swap. It leaves the
// former output under stage, where the caller removes it after success. There
// is no interval in which output is missing and no failure after publication.
// If the filesystem/kernel lacks exchange support, fail closed rather than
// degrading to a sequence of partially visible per-file writes.
func publishDirectory(stage, output string) error {
	if filepath.Dir(stage) != filepath.Dir(output) {
		return fmt.Errorf("stage and output must share a parent")
	}
	parent, err := openDirectory(filepath.Dir(output))
	if err != nil {
		return err
	}
	defer parent.Close()
	err = unix.Renameat2(int(parent.Fd()), filepath.Base(stage), int(parent.Fd()), filepath.Base(output), unix.RENAME_EXCHANGE)
	runtime.KeepAlive(parent)
	if err != nil {
		return fmt.Errorf("atomic directory exchange: %w", err)
	}
	return nil
}
