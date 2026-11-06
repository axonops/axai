package fsutil_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/axonops/axai/internal/fsutil"
)

func TestWriteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	require.NoError(t, fsutil.WriteFile(path, []byte("one"), 0o600))
	require.NoError(t, fsutil.WriteFile(path, []byte("two"), 0o640))

	b, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "two", string(b))
	fi, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o640), fi.Mode().Perm())

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestWriteFileFailureLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	// Renaming a file over a non-empty directory fails after the temp is written.
	target := filepath.Join(dir, "target")
	require.NoError(t, os.MkdirAll(filepath.Join(target, "child"), 0o750))
	require.Error(t, fsutil.WriteFile(target, []byte("x"), 0o600))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "target", entries[0].Name())
}

func TestWriteFileMissingDir(t *testing.T) {
	require.Error(t, fsutil.WriteFile(filepath.Join(t.TempDir(), "no", "f"), nil, 0o600))
}
