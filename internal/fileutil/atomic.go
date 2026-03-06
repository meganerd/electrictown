// Package fileutil provides safe file I/O helpers.
package fileutil

import (
	"os"
	"path/filepath"
)

// AtomicWrite writes data to path atomically using a temp file + rename.
// The temp file is created alongside the target so the rename is always
// on the same filesystem (guaranteed atomic on POSIX). On failure, the
// temp file is removed and the original file is left untouched.
func AtomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}

	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp) // best-effort cleanup
		return err
	}
	return nil
}
