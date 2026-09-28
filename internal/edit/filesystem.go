package edit

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

func cleanRoot(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("snapshot directory must not be empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if runtime.GOOS == "darwin" {
		// macOS exposes its standard temporary directory through /var, a
		// symlink to /private/var. Resolve argument roots once; subsequent
		// descriptor-relative traversal still refuses symlink replacement.
		abs, err = filepath.EvalSymlinks(abs)
		if err != nil {
			return "", fmt.Errorf("snapshot root %q: %w", path, err)
		}
	}
	// Opening every directory component with NOFOLLOW also rejects symlinked
	// ancestors, not only symlinks at the root argument itself.
	f, err := openDirectory(abs)
	if err != nil {
		return "", fmt.Errorf("snapshot root %q: %w", path, err)
	}
	f.Close()
	if abs == string(filepath.Separator) {
		return "", fmt.Errorf("filesystem root is not a snapshot directory")
	}
	return abs, nil
}
func overlaps(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+string(filepath.Separator)) || strings.HasPrefix(b, a+string(filepath.Separator))
}
func snapshot(path string) (tree, error) {
	dir, err := openDirectory(path)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	t := tree{}
	var walk func(*os.File, string) error
	walk = func(dir *os.File, prefix string) error {
		entries, err := dir.ReadDir(-1)
		if err != nil {
			return err
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, de := range entries {
			name := de.Name()
			p := filepath.Join(prefix, name)
			mode, err := readChildMode(dir, name)
			if err != nil {
				return fmt.Errorf("%q: %w", p, err)
			}
			switch {
			case mode.IsDir():
				child, err := openChildDirectory(dir, name)
				if err != nil {
					return fmt.Errorf("%q: %w", p, err)
				}
				err = walk(child, p)
				child.Close()
				if err != nil {
					return err
				}
			case mode&os.ModeSymlink != 0:
				target, err := readChildLink(dir, name)
				if err != nil {
					return fmt.Errorf("%q: %w", p, err)
				}
				t[p] = entry{data: []byte(target), mode: mode, link: true}
			case mode.IsRegular():
				f, err := openChildFile(dir, name)
				if err != nil {
					return fmt.Errorf("%q: %w", p, err)
				}
				actual, err := f.Stat()
				if err != nil {
					f.Close()
					return err
				}
				if !actual.Mode().IsRegular() {
					f.Close()
					return fmt.Errorf("%q changed type during snapshot", p)
				}
				data, err := io.ReadAll(f)
				f.Close()
				if err != nil {
					return fmt.Errorf("%q: %w", p, err)
				}
				t[p] = entry{data: data, mode: actual.Mode()}
			default:
				return fmt.Errorf("unsupported file type at %q (only regular files, directories and symlinks are supported)", p)
			}
		}
		return nil
	}
	if err = walk(dir, ""); err != nil {
		return nil, err
	}
	return t, nil
}
func validateTree(t tree) error {
	for p := range t {
		if p == "." || p == "" || filepath.IsAbs(p) || filepath.Clean(p) != p || p == ".." || strings.HasPrefix(p, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe output pathname %q", p)
		}
		for parent := filepath.Dir(p); parent != "."; parent = filepath.Dir(parent) {
			if _, ok := t[parent]; ok {
				return fmt.Errorf("selected changes cannot represent both file %q and descendant %q; select the corresponding deletion too", parent, p)
			}
		}
	}
	return nil
}
func writeTree(root string, t tree) error {
	paths := make([]string, 0, len(t))
	for p := range t {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	// The stage is private and empty. Validate first, then create all parent
	// directories before any symlinks; no snapshot path is ever traversed here.
	for _, p := range paths {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0755); err != nil {
			return err
		}
	}
	for _, p := range paths {
		e := t[p]
		name := filepath.Join(root, p)
		if e.link {
			if err := os.Symlink(string(e.data), name); err != nil {
				return err
			}
			continue
		}
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, err = f.Write(e.data)
		if err == nil {
			err = f.Chmod(e.mode.Perm())
		}
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

// After an exchange, stage contains the former output. jj can make snapshot
// directories read-only, so ordinary RemoveAll may leave that old tree behind.
// Only change directory permissions through pinned no-follow descriptors; never
// chmod a symlink target. Cleanup errors cannot turn a successful publication
// into an error (which would falsely promise the output was unchanged).
func removeStage(path string) {
	root, err := openDirectory(path)
	if err != nil {
		_ = os.Remove(path)
		return
	}
	var writable func(*os.File)
	writable = func(dir *os.File) {
		_ = dir.Chmod(0700)
		entries, err := dir.ReadDir(-1)
		if err != nil {
			return
		}
		for _, e := range entries {
			mode, err := readChildMode(dir, e.Name())
			if err != nil || !mode.IsDir() {
				continue
			}
			child, err := openChildDirectory(dir, e.Name())
			if err == nil {
				writable(child)
				child.Close()
			}
		}
	}
	writable(root)
	root.Close()
	_ = os.RemoveAll(path)
}
