//go:build linux || darwin

package edit

import (
	"os"
	"runtime"

	"golang.org/x/sys/unix"
)

// DirEntry.Info may lstat using the directory's original pathname. Use fstatat
// instead, so a renamed directory or symlink substituted in its old location
// can never redirect even the metadata part of a snapshot walk.
func readChildMode(dir *os.File, name string) (os.FileMode, error) {
	var st unix.Stat_t
	err := unix.Fstatat(int(dir.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW)
	runtime.KeepAlive(dir)
	if err != nil {
		return 0, err
	}
	mode := os.FileMode(st.Mode & 0777)
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFREG:
	case unix.S_IFDIR:
		mode |= os.ModeDir
	case unix.S_IFLNK:
		mode |= os.ModeSymlink
	default:
		mode |= os.ModeIrregular
	}
	return mode, nil
}
