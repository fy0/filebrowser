//go:build linux

package fbhttp

import (
	"os"

	"golang.org/x/sys/unix"
)

func adviseFileSequential(file *os.File) {
	_ = unix.Fadvise(int(file.Fd()), 0, 0, unix.FADV_SEQUENTIAL)
}

func adviseFileDontNeed(file *os.File, offset, length int64) {
	_ = unix.Fadvise(int(file.Fd()), offset, length, unix.FADV_DONTNEED)
}
