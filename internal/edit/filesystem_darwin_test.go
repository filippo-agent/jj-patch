//go:build darwin

package edit

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func darwinTempRoot(t *testing.T) string {
	t.Helper()
	// t.TempDir commonly returns /var/folders/... on macOS. Resolve the test
	// root once, retaining no-follow behavior in the helpers under test.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func darwinWriteFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func darwinCheckFile(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != want {
		t.Fatalf("ReadFile(%q) = %q, %v; want %q", path, data, err, want)
	}
}

func TestDarwinOpenNoFollow(t *testing.T) {
	root := darwinTempRoot(t)
	if err := os.MkdirAll(filepath.Join(root, "real", "child"), 0700); err != nil {
		t.Fatal(err)
	}
	darwinWriteFile(t, filepath.Join(root, "file"), "content")
	for name, target := range map[string]string{"directory-link": "real", "file-link": "file"} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{filepath.Join(root, "directory-link"), filepath.Join(root, "directory-link", "child")} {
		f, err := openDirectory(path)
		if err == nil {
			f.Close()
			t.Fatalf("openDirectory(%q) followed a symlink", path)
		}
	}
	dir, err := openDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if f, err := openChildDirectory(dir, "directory-link"); err == nil {
		f.Close()
		t.Fatal("openChildDirectory followed a symlink")
	}
	if f, err := openChildFile(dir, "file-link"); err == nil {
		f.Close()
		t.Fatal("openChildFile followed a symlink")
	}
	child, err := openChildDirectory(dir, "real")
	if err != nil {
		t.Fatal(err)
	}
	child.Close()
}

func TestDarwinChildrenUseDirectoryDescriptor(t *testing.T) {
	root := darwinTempRoot(t)
	original := filepath.Join(root, "original")
	moved := filepath.Join(root, "moved")
	if err := os.MkdirAll(filepath.Join(original, "child"), 0700); err != nil {
		t.Fatal(err)
	}
	darwinWriteFile(t, filepath.Join(original, "file"), "original")
	// This exceeds readChildLink's initial buffer and remains below PATH_MAX.
	target := strings.Repeat("segment/", 75) + "missing"
	if err := os.Symlink(target, filepath.Join(original, "link")); err != nil {
		t.Fatal(err)
	}
	dir, err := openDirectory(original)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if err := os.Rename(original, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(original, 0700); err != nil {
		t.Fatal(err)
	}
	darwinWriteFile(t, filepath.Join(original, "file"), "replacement")
	f, err := openChildFile(dir, "file")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(f)
	f.Close()
	if err != nil || string(data) != "original" {
		t.Fatalf("descriptor-relative read = %q, %v", data, err)
	}
	child, err := openChildDirectory(dir, "child")
	if err != nil {
		t.Fatal(err)
	}
	child.Close()
	got, err := readChildLink(dir, "link")
	if err != nil || got != target {
		t.Fatalf("descriptor-relative readlink = %q, %v; want %q", got, err, target)
	}
}

func TestDarwinPublishDirectorySwap(t *testing.T) {
	root := darwinTempRoot(t)
	stage, output := filepath.Join(root, "stage"), filepath.Join(root, "output")
	for _, path := range []string{stage, output} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	darwinWriteFile(t, filepath.Join(stage, "new"), "new contents")
	darwinWriteFile(t, filepath.Join(output, "old"), "old contents")
	if err := publishDirectory(stage, output); err != nil {
		if errors.Is(err, unix.ENOTSUP) {
			darwinCheckFile(t, filepath.Join(stage, "new"), "new contents")
			darwinCheckFile(t, filepath.Join(output, "old"), "old contents")
			t.Skipf("test filesystem does not support atomic exchange: %v", err)
		}
		t.Fatal(err)
	}
	darwinCheckFile(t, filepath.Join(output, "new"), "new contents")
	darwinCheckFile(t, filepath.Join(stage, "old"), "old contents")
	for _, path := range []string{filepath.Join(output, "old"), filepath.Join(stage, "new")} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("old location %q still exists: %v", path, err)
		}
	}
}

func TestDarwinPublishDirectoryFailuresPreserveEntries(t *testing.T) {
	root := darwinTempRoot(t)
	stage, output := filepath.Join(root, "stage"), filepath.Join(root, "output")
	if err := os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	darwinWriteFile(t, filepath.Join(stage, "new"), "new contents")
	if err := publishDirectory(stage, output); err == nil {
		t.Fatal("exchange with missing output succeeded")
	}
	darwinCheckFile(t, filepath.Join(stage, "new"), "new contents")
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing output was published: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "different", "output"), 0700); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(root, "different", "output")
	darwinWriteFile(t, filepath.Join(other, "old"), "old contents")
	if err := publishDirectory(stage, other); err == nil {
		t.Fatal("exchange across different parents succeeded")
	}
	darwinCheckFile(t, filepath.Join(stage, "new"), "new contents")
	darwinCheckFile(t, filepath.Join(other, "old"), "old contents")
	if err := os.Mkdir(output, 0700); err != nil {
		t.Fatal(err)
	}
	darwinWriteFile(t, filepath.Join(output, "old"), "old contents")
	if err := os.Symlink(".", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := publishDirectory(filepath.Join(root, "alias", "stage"), filepath.Join(root, "alias", "output")); err == nil {
		t.Fatal("exchange through symlink parent succeeded")
	}
	darwinCheckFile(t, filepath.Join(stage, "new"), "new contents")
	darwinCheckFile(t, filepath.Join(output, "old"), "old contents")
}
