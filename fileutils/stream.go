package fileutils

import (
	"errors"
	"fmt"
	"io"
	"log"

	"github.com/spf13/afero"
)

const (
	StreamBufferSize = 32 * 1024
	// Both input and output can retain cache. Flush in small batches so a
	// 64 MiB, swapless container still has room for the application's heap.
	streamCacheDropEvery = 2 * 1024 * 1024
)

// CopyWithCacheControl bounds both userspace buffers and dirty file cache.
// Linux charges page cache to the container as well as the Go heap. Merely
// using io.CopyBuffer does not bound this memory during uploads or checksums.
// buf may be reused across files; onWrite and sourceDrop are optional.
func CopyWithCacheControl(dst io.Writer, src io.Reader, buf []byte, onWrite func(int), sourceDrop func()) (int64, error) {
	if len(buf) == 0 {
		buf = make([]byte, StreamBufferSize)
	}
	if len(buf) > StreamBufferSize {
		buf = buf[:StreamBufferSize]
	}
	writer := &cacheWriter{dst: dst, onWrite: onWrite, sourceDrop: sourceDrop}
	writer.file, _ = dst.(afero.File)
	writer.source, _ = src.(afero.File)
	// Hide WriterTo: io.CopyBuffer otherwise ignores buf for optimized readers.
	written, err := io.CopyBuffer(writer, struct{ io.Reader }{src}, buf)
	return written, errors.Join(err, writer.flush())
}

type cacheWriter struct {
	dst          io.Writer
	file         afero.File
	source       afero.File
	onWrite      func(int)
	sourceDrop   func()
	pending      int64
	cacheWarning bool
}

func (w *cacheWriter) Write(p []byte) (int, error) {
	n, err := w.dst.Write(p)
	if n > 0 {
		w.pending += int64(n)
		if w.onWrite != nil {
			w.onWrite(n)
		}
		if w.pending >= streamCacheDropEvery {
			err = errors.Join(err, w.flush())
		}
	}
	return n, err
}

func (w *cacheWriter) flush() error {
	if w.pending == 0 {
		return nil
	}
	if w.file != nil {
		// Dirty pages cannot be discarded until writeback completes. An fsync
		// failure must stop the restore, rather than report success and keep
		// accumulating dirty cache (or delete the user's old data).
		if err := w.file.Sync(); err != nil {
			return fmt.Errorf("sync copied file: %w", err)
		}
		w.drop(w.file)
	}
	if w.source != nil {
		w.drop(w.source)
	}
	if w.sourceDrop != nil {
		w.sourceDrop()
	}
	w.pending = 0
	return nil
}

func (w *cacheWriter) drop(file afero.File) {
	if err := DropFileCache(file); err != nil && !w.cacheWarning {
		// Advice is best effort (some filesystems do not support it), but a
		// failure should be visible when diagnosing container memory pressure.
		log.Printf("stream copy: cannot discard file cache for %q: %v", file.Name(), err)
		w.cacheWarning = true
	}
}
