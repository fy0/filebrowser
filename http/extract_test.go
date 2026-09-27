package fbhttp

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/spf13/afero"

	"github.com/filebrowser/filebrowser/v2/files"
)

func TestExtractArchiveContents(t *testing.T) {
	payload := bytes.Repeat([]byte("backup-database-page"), (8<<20)/20+1)
	wantHash := sha256.Sum256(payload)
	for _, format := range []string{"zip", "tar", "tar.gz"} {
		t.Run(format, func(t *testing.T) {
			root := t.TempDir()
			archivePath := "backup." + format
			archive, err := os.Create(filepath.Join(root, archivePath))
			if err != nil {
				t.Fatal(err)
			}
			if format == "zip" {
				zw := zip.NewWriter(archive)
				for _, name := range []string{"data/first.db", "data/nested/second.db"} {
					entry, err := zw.Create(name)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = entry.Write(payload); err != nil {
						t.Fatal(err)
					}
				}
				if err = zw.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				var output io.Writer = archive
				var gz *gzip.Writer
				if format == "tar.gz" {
					gz = gzip.NewWriter(archive)
					output = gz
				}
				tw := tar.NewWriter(output)
				for _, name := range []string{"data/first.db", "data/nested/second.db"} {
					if err = tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(payload)), Mode: 0o600}); err != nil {
						t.Fatal(err)
					}
					if _, err = tw.Write(payload); err != nil {
						t.Fatal(err)
					}
				}
				if err = tw.Close(); err != nil {
					t.Fatal(err)
				}
				if gz != nil {
					if err = gz.Close(); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err = archive.Close(); err != nil {
				t.Fatal(err)
			}
			afs := afero.NewBasePathFs(afero.NewOsFs(), root)
			extract := extractZip
			if format == "tar" {
				extract = extractTar
			}
			if format == "tar.gz" {
				extract = extractTarGz
			}
			if err = extract(afs, archivePath, "out", 0o600, 0o700, time.Now()); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"out/data/first.db", "out/data/nested/second.db"} {
				file, err := afs.Open(name)
				if err != nil {
					t.Fatal(err)
				}
				hash := sha256.New()
				n, err := io.Copy(hash, file)
				_ = file.Close()
				if err != nil || n != int64(len(payload)) || !bytes.Equal(hash.Sum(nil), wantHash[:]) {
					t.Fatalf("restored %s: size=%d error=%v checksum=%x", name, n, err, hash.Sum(nil))
				}
			}
		})
	}
}

type failingSyncFile struct {
	afero.File
	err error
}

func (f failingSyncFile) Sync() error { return f.err }

func TestCopyWithCacheControlSyncError(t *testing.T) {
	afs := afero.NewMemMapFs()
	file, err := afs.Create("output")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	want := errors.New("disk writeback failed")
	progress := newExtractProgress("backup.zip", "out", time.Now(), 1, 1)
	_, err = copyWithCacheControl(failingSyncFile{File: file, err: want}, io.LimitReader(zeroReader{}, 1), progress, nil)
	if !errors.Is(err, want) {
		t.Fatalf("copy error = %v, want sync failure %v", err, want)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// Run explicitly on Linux, preferably in a 64 MiB memory cgroup without swap. The
// fixture and restored data are streamed; their contents never enter the heap.
// FILEBROWSER_RESTORE_SIZE_MIB controls the expanded size (default 256),
// FILEBROWSER_RESTORE_METHOD selects store or deflate (default store), and
// FILEBROWSER_RESTORE_BASELINE_MIB adds resident memory to simulate other work.
func TestBackupRestoreMemory(t *testing.T) {
	if os.Getenv("FILEBROWSER_RESTORE_STRESS") != "1" {
		t.Skip("set FILEBROWSER_RESTORE_STRESS=1 to run the large restore test")
	}
	sizeMiB := restoreStressInt(t, "FILEBROWSER_RESTORE_SIZE_MIB", 256)
	if sizeMiB < 1 {
		t.Fatal("expanded size must be positive")
	}
	size := int64(sizeMiB) << 20
	baselineMiB := restoreStressInt(t, "FILEBROWSER_RESTORE_BASELINE_MIB", 0)
	method := uint16(zip.Store)
	switch os.Getenv("FILEBROWSER_RESTORE_METHOD") {
	case "", "store":
	case "deflate":
		method = zip.Deflate
	default:
		t.Fatal("FILEBROWSER_RESTORE_METHOD must be store or deflate")
	}
	root := t.TempDir()
	afs := afero.NewBasePathFs(afero.NewOsFs(), root)
	archive, err := os.Create(filepath.Join(root, "source.zip"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = archive.Close() })
	zw := zip.NewWriter(archive)
	entry, err := zw.CreateHeader(&zip.FileHeader{Name: "data/database.db", Method: method})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.CopyBuffer(entry, io.LimitReader(zeroReader{}, size), make([]byte, 32<<10)); err != nil {
		t.Fatal(err)
	}
	if err = zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err = archive.Sync(); err != nil {
		t.Fatal(err)
	}
	adviseFileDontNeed(archive, 0, 0)
	if _, err = archive.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	baseline := make([]byte, baselineMiB<<20)
	for i := 0; i < len(baseline); i += os.Getpagesize() {
		baseline[i] = 1
	}
	defer func() { runtime.KeepAlive(baseline) }()
	t.Logf("restore workload: expanded=%d MiB method=%d extraResident=%d MiB", sizeMiB, method, baselineMiB)
	start := time.Now()
	logExtractMemory("before-upload", "uploaded.zip", "out", start)
	if _, err = writeFile(afs, "uploaded.zip", archive, 0o600, 0o700); err != nil {
		t.Fatal(err)
	}
	adviseFileDontNeed(archive, 0, 0)
	logExtractMemory("after-upload", "uploaded.zip", "out", start)
	info := &files.FileInfo{Fs: afs, Path: "uploaded.zip"}
	if err = info.Checksum("md5"); err != nil {
		t.Fatal(err)
	}
	logExtractMemory("after-checksum", "uploaded.zip", "out", start)
	if err = extractZip(afs, "uploaded.zip", "out", 0o600, 0o700, start); err != nil {
		t.Fatal(err)
	}
	logExtractMemory("after-extract", "uploaded.zip", "out", start)
	restored, err := afs.Stat("out/data/database.db")
	if err != nil {
		t.Fatal(err)
	}
	if restored.Size() != size {
		t.Fatalf("restored size = %d, want %d", restored.Size(), size)
	}
}

func restoreStressInt(t *testing.T, name string, fallback int) int {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		t.Fatalf("invalid %s=%q", name, raw)
	}
	return value
}
