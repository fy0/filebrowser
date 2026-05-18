//go:build !linux

package fbhttp

import "os"

func adviseFileSequential(_ *os.File) {
}

func adviseFileDontNeed(_ *os.File, _, _ int64) {
}

func advisePathDontNeed(_ string) {
}
