package fileutils

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/spf13/afero"
)

type streamTestFile struct {
	afero.File
	written  int
	maxWrite int
	syncs    int
	syncErr  error
}

func (f *streamTestFile) Write(p []byte) (int, error) {
	f.written += len(p)
	f.maxWrite = max(f.maxWrite, len(p))
	return len(p), nil
}

func (f *streamTestFile) Sync() error {
	f.syncs++
	return f.syncErr
}

type streamTestReader struct {
	remaining    int
	usedWriterTo bool
	err          error
}

func (r *streamTestReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		if r.err != nil {
			return 0, r.err
		}
		return 0, io.EOF
	}
	n := min(len(p), r.remaining)
	clear(p[:n])
	r.remaining -= n
	return n, nil
}

func (r *streamTestReader) WriteTo(_ io.Writer) (int64, error) {
	r.usedWriterTo = true
	return 0, errors.New("WriterTo bypassed the copy buffer")
}

func TestCopyWithCacheControlBoundsWrites(t *testing.T) {
	const size = 2*streamCacheDropEvery + 17
	for _, bufferSize := range []int{0, 512, StreamBufferSize, 2 * StreamBufferSize} {
		dst := &streamTestFile{}
		src := &streamTestReader{remaining: size}
		progress, drops := 0, 0
		n, err := CopyWithCacheControl(dst, src, make([]byte, bufferSize), func(n int) { progress += n }, func() { drops++ })
		if err != nil {
			t.Fatal(err)
		}
		if n != size || dst.written != size || progress != size {
			t.Fatalf("copy sizes = %d/%d/%d, want %d", n, dst.written, progress, size)
		}
		if src.usedWriterTo || dst.maxWrite > StreamBufferSize {
			t.Fatalf("unbounded copy: WriterTo=%v maxWrite=%d", src.usedWriterTo, dst.maxWrite)
		}
		if dst.syncs != 3 || drops != 3 {
			t.Fatalf("syncs/drops = %d/%d, want 3/3", dst.syncs, drops)
		}
	}
}

func TestCopyWithCacheControlStopsOnSyncError(t *testing.T) {
	want := errors.New("disk full during writeback")
	for _, size := range []int{1, 2 * streamCacheDropEvery} {
		dst := &streamTestFile{syncErr: want}
		n, err := CopyWithCacheControl(dst, &streamTestReader{remaining: size}, nil, nil, nil)
		if !errors.Is(err, want) {
			t.Fatalf("error = %v, want %v", err, want)
		}
		if n > streamCacheDropEvery {
			t.Fatalf("copied %d bytes after writeback failed", n)
		}
	}
}

func TestCopyWithCacheControlFlushesOnReadError(t *testing.T) {
	want := errors.New("truncated input")
	dst := &streamTestFile{}
	n, err := CopyWithCacheControl(dst, &streamTestReader{remaining: 17, err: want}, nil, nil, nil)
	if n != 17 || !errors.Is(err, want) || dst.syncs != 1 {
		t.Fatalf("copy = (%d, %v), syncs = %d", n, err, dst.syncs)
	}
}

func TestCopyWithCacheControlContents(t *testing.T) {
	want := bytes.Repeat([]byte("backup data"), StreamBufferSize)
	var dst bytes.Buffer
	n, err := CopyWithCacheControl(&dst, bytes.NewReader(want), nil, nil, nil)
	if err != nil || n != int64(len(want)) || !bytes.Equal(dst.Bytes(), want) {
		t.Fatalf("copy = (%d, %v), contents equal = %v", n, err, bytes.Equal(dst.Bytes(), want))
	}
}
