//go:build !linux

package fileutils

import "github.com/spf13/afero"

func DropFileCache(_ afero.File) error { return nil }
