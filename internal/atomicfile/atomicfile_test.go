package atomicfile

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func onlyEntry(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("directory holds %v, want just the written file (no leftover temp file)", names)
	}
	return entries[0].Name()
}

func TestWriteCreatesANewFileWithPermLessUmask(t *testing.T) {
	old := syscall.Umask(0o022)
	defer syscall.Umask(old)
	dir := t.TempDir()
	p := filepath.Join(dir, "a.org")

	if err := Write(p, []byte("hello\n"), 0o666); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(p)
	if err != nil || string(got) != "hello\n" {
		t.Fatalf("content = %q, %v; want %q", got, err, "hello\n")
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644 (0666 less umask 022)", fi.Mode().Perm())
	}
	if name := onlyEntry(t, dir); name != "a.org" {
		t.Errorf("entry = %q, want a.org", name)
	}
}

func TestWriteKeepsAnExistingFilesMode(t *testing.T) {
	for _, mode := range []os.FileMode{0o600, 0o640, 0o755} {
		dir := t.TempDir()
		p := filepath.Join(dir, "a.org")
		if err := os.WriteFile(p, []byte("old\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}

		if err := Write(p, []byte("new\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		got, _ := os.ReadFile(p)
		if string(got) != "new\n" {
			t.Errorf("content = %q, want new", got)
		}
		fi, _ := os.Stat(p)
		if fi.Mode().Perm() != mode {
			t.Errorf("mode = %v, want %v kept", fi.Mode().Perm(), mode)
		}
		onlyEntry(t, dir)
	}
}

func TestWriteFailsWithoutTheDirectory(t *testing.T) {
	p := filepath.Join(t.TempDir(), "missing", "a.org")
	if err := Write(p, []byte("x"), 0o644); err == nil {
		t.Fatal("Write into a missing directory succeeded, want an error")
	}
}

func TestWriteFailureLeavesTheFileAndNoTempBehind(t *testing.T) {
	dir := t.TempDir()
	// A directory where the file should be: the rename fails.
	p := filepath.Join(dir, "a.org")
	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "keep"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Write(p, []byte("x"), 0o644); err == nil {
		t.Fatal("Write over a non-empty directory succeeded, want an error")
	}
	if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
		t.Errorf("target changed: %v, %v", fi, err)
	}
	onlyEntry(t, dir)
}

func TestTempNamesAreUnique(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")
	seen := map[string]bool{}
	for range 50 {
		f, err := createTemp(p, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
		if seen[f.Name()] {
			t.Fatalf("temp name %s reused", f.Name())
		}
		seen[f.Name()] = true
	}
}
