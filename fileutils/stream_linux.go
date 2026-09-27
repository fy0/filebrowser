//go:build linux

package fileutils

import (
	"os"

	"github.com/spf13/afero"
	"golang.org/x/sys/unix"
)

// DropFileCache uses the already-open descriptor. Reopening a path can fail
// for write-only files, or advise a different inode after a rename. Unwrap
// BasePathFile explicitly: afero.File does not expose the embedded os.File.Fd.
func DropFileCache(file afero.File) error {
	for {
		if scoped, ok := file.(*afero.BasePathFile); ok {
			file = scoped.File
			continue
		}
		break
	}
	if native, ok := file.(*os.File); ok {
		return unix.Fadvise(int(native.Fd()), 0, 0, unix.FADV_DONTNEED)
	}
	return nil
}
