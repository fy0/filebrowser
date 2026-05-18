package fbhttp

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/afero"
	"golang.org/x/text/encoding/simplifiedchinese"

	"github.com/filebrowser/filebrowser/v2/files"
)

const (
	extractCopyBufferSize = 32 * 1024
	extractCacheDropEvery = 8 * 1024 * 1024
	extractLogEvery       = 64 * 1024 * 1024
)

// extractHandler handles archive extraction requests
// POST /api/extract/{path}?destination=...&mode=...
// mode: "here" (extract to same directory) or "subdir" (extract to subdirectory named after archive)
var extractHandler = withUser(func(_ http.ResponseWriter, r *http.Request, d *data) (int, error) {
	if !d.user.Perm.Create {
		return http.StatusForbidden, nil
	}

	archivePath := r.URL.Path

	// Get file info
	file, err := files.NewFileInfo(&files.FileOptions{
		Fs:         d.user.Fs,
		Path:       archivePath,
		Modify:     d.user.Perm.Modify,
		Expand:     false,
		ReadHeader: d.server.TypeDetectionByHeader,
		Checker:    d,
	})
	if err != nil {
		return errToStatus(err), err
	}

	if file.IsDir {
		return http.StatusBadRequest, errors.New("cannot extract a directory")
	}

	// Determine destination directory
	mode := r.URL.Query().Get("mode")
	destination := r.URL.Query().Get("destination")

	if destination != "" {
		destination, err = url.QueryUnescape(destination)
		if err != nil {
			return http.StatusBadRequest, err
		}
	} else if mode == "subdir" {
		// Extract to subdirectory named after archive (without extension)
		baseName := path.Base(archivePath)
		dirName := getArchiveBaseName(baseName)
		destination = path.Join(path.Dir(archivePath), dirName)
	} else {
		// Default: extract to same directory as the archive
		destination = path.Dir(archivePath)
	}

	// Check destination permission
	if !d.Check(destination) {
		return http.StatusForbidden, nil
	}

	// Ensure destination exists
	if err := d.user.Fs.MkdirAll(destination, d.settings.DirMode); err != nil {
		return errToStatus(err), err
	}

	start := time.Now()
	logExtractMemory("start", archivePath, destination, start)

	// Determine archive type and extract
	lowerPath := strings.ToLower(archivePath)
	switch {
	case strings.HasSuffix(lowerPath, ".zip"):
		err = extractZip(d.user.Fs, archivePath, destination, d.settings.FileMode, d.settings.DirMode, start)
	case strings.HasSuffix(lowerPath, ".tar.gz") || strings.HasSuffix(lowerPath, ".tgz"):
		err = extractTarGz(d.user.Fs, archivePath, destination, d.settings.FileMode, d.settings.DirMode, start)
	case strings.HasSuffix(lowerPath, ".tar"):
		err = extractTar(d.user.Fs, archivePath, destination, d.settings.FileMode, d.settings.DirMode, start)
	default:
		return http.StatusBadRequest, errors.New("unsupported archive format: only .zip, .tar.gz, .tgz, and .tar are supported")
	}

	if err != nil {
		return errToStatus(err), err
	}

	logExtractMemory("done", archivePath, destination, start)

	return http.StatusOK, nil
})

func logExtractMemory(stage, archivePath, destination string, start time.Time) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	log.Printf(
		"backup restore extract %s archive=%q destination=%q elapsed=%s alloc=%s heapSys=%s sys=%s numGC=%d cgroup=%q",
		stage,
		archivePath,
		destination,
		time.Since(start).Round(time.Millisecond),
		formatBytes(mem.Alloc),
		formatBytes(mem.HeapSys),
		formatBytes(mem.Sys),
		mem.NumGC,
		readCgroupMemorySummary(),
	)
}

func logExtractProgress(progress *extractProgress, stage string) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	log.Printf(
		"backup restore extract %s archive=%q destination=%q elapsed=%s files=%d/%d bytes=%s/%s alloc=%s heapSys=%s sys=%s numGC=%d cgroup=%q",
		stage,
		progress.archivePath,
		progress.destination,
		time.Since(progress.start).Round(time.Millisecond),
		progress.files,
		progress.totalFiles,
		formatBytes(progress.bytes),
		formatBytes(progress.totalBytes),
		formatBytes(mem.Alloc),
		formatBytes(mem.HeapSys),
		formatBytes(mem.Sys),
		mem.NumGC,
		readCgroupMemorySummary(),
	)
}

func readCgroupMemorySummary() string {
	current := readCgroupMemoryValue("/sys/fs/cgroup/memory.current")
	if current == "" {
		current = readCgroupMemoryValue("/sys/fs/cgroup/memory/memory.usage_in_bytes")
	}
	if current == "" {
		return "n/a"
	}

	statPath := "/sys/fs/cgroup/memory.stat"
	if _, err := os.Stat(statPath); err != nil {
		statPath = "/sys/fs/cgroup/memory/memory.stat"
	}

	anon, file := "", ""
	if data, err := os.ReadFile(statPath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 {
				continue
			}
			switch fields[0] {
			case "anon", "total_inactive_anon":
				if anon == "" {
					anon = formatCgroupBytes(fields[1])
				}
			case "file", "total_cache":
				if file == "" {
					file = formatCgroupBytes(fields[1])
				}
			}
		}
	}

	parts := []string{"current=" + current}
	if anon != "" {
		parts = append(parts, "anon="+anon)
	}
	if file != "" {
		parts = append(parts, "file="+file)
	}
	return strings.Join(parts, " ")
}

func readCgroupMemoryValue(filePath string) string {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return ""
	}
	return formatCgroupBytes(strings.TrimSpace(string(data)))
}

func formatCgroupBytes(value string) string {
	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return value
	}
	return formatBytes(n)
}

func formatBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := uint64(unit), 0
	for value := n / unit; value >= unit; value /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

type extractProgress struct {
	archivePath string
	destination string
	start       time.Time
	totalFiles  int
	totalBytes  uint64
	files       int
	bytes       uint64
	lastBytes   uint64
	lastLog     time.Time
}

func newExtractProgress(archivePath, destination string, start time.Time, totalFiles int, totalBytes uint64) *extractProgress {
	return &extractProgress{
		archivePath: archivePath,
		destination: destination,
		start:       start,
		totalFiles:  totalFiles,
		totalBytes:  totalBytes,
		lastLog:     start,
	}
}

func (p *extractProgress) addBytes(n int) {
	if n <= 0 {
		return
	}
	p.bytes += uint64(n)
	if p.bytes-p.lastBytes >= extractLogEvery || time.Since(p.lastLog) >= 10*time.Second {
		p.lastBytes = p.bytes
		p.lastLog = time.Now()
		logExtractProgress(p, "progress")
	}
}

func (p *extractProgress) finishFile() {
	p.files++
	if p.files%50 == 0 || p.files == p.totalFiles {
		p.lastBytes = p.bytes
		p.lastLog = time.Now()
		logExtractProgress(p, "progress")
	}
}

// getArchiveBaseName removes archive extensions from filename
func getArchiveBaseName(filename string) string {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"):
		return filename[:len(filename)-7]
	case strings.HasSuffix(lower, ".tgz"):
		return filename[:len(filename)-4]
	case strings.HasSuffix(lower, ".tar"):
		return filename[:len(filename)-4]
	case strings.HasSuffix(lower, ".zip"):
		return filename[:len(filename)-4]
	default:
		return filename
	}
}

// extractZip extracts a ZIP archive using streaming (low memory usage)
func extractZip(afs afero.Fs, archivePath, destination string, fileMode, dirMode os.FileMode, start time.Time) error {
	// Get the real path for zip.OpenReader
	realPath := archivePath
	if bpfs, ok := afs.(*afero.BasePathFs); ok {
		realPath = afero.FullBaseFsPath(bpfs, archivePath)
	}

	archiveFile, err := os.Open(realPath)
	if err != nil {
		return fmt.Errorf("failed to open zip file: %w", err)
	}
	defer archiveFile.Close()
	adviseFileSequential(archiveFile)

	info, err := archiveFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat zip file: %w", err)
	}

	zipReader, err := zip.NewReader(archiveFile, info.Size())
	if err != nil {
		return fmt.Errorf("failed to open zip file: %w", err)
	}

	totalFiles, totalBytes := 0, uint64(0)
	for _, f := range zipReader.File {
		if !f.FileInfo().IsDir() {
			totalFiles++
			totalBytes += f.UncompressedSize64
		}
	}
	progress := newExtractProgress(archivePath, destination, start, totalFiles, totalBytes)
	dropArchiveCache := func() {
		adviseFileDontNeed(archiveFile, 0, 0)
	}

	for _, f := range zipReader.File {
		if err := extractZipFile(afs, f, destination, fileMode, dirMode, progress, dropArchiveCache); err != nil {
			return err
		}
		dropArchiveCache()
	}

	return nil
}

// decodeZipFileName attempts to decode a zip file name that may be GBK encoded
func decodeZipFileName(name string, flags uint16) string {
	// If UTF-8 flag is set (bit 11), the name is already UTF-8
	if flags&(1<<11) != 0 {
		return name
	}

	// Check if the name is valid UTF-8
	if utf8.ValidString(name) {
		// Check if it contains any non-ASCII characters that look like valid UTF-8
		hasNonASCII := false
		for _, r := range name {
			if r > 127 {
				hasNonASCII = true
				break
			}
		}
		// If it's pure ASCII or looks like valid UTF-8 Chinese, use as-is
		if !hasNonASCII {
			return name
		}
	}

	// Try to decode as GBK
	decoded, err := simplifiedchinese.GBK.NewDecoder().String(name)
	if err != nil {
		return name // Return original if decoding fails
	}

	// Verify the decoded string is valid UTF-8
	if utf8.ValidString(decoded) {
		return decoded
	}

	return name
}

// extractZipFile extracts a single file from a ZIP archive
func extractZipFile(afs afero.Fs, f *zip.File, destination string, fileMode, dirMode os.FileMode, progress *extractProgress, sourceDrop func()) error {
	// Decode file name (handle GBK encoding)
	fileName := decodeZipFileName(f.Name, f.Flags)

	// Normalize path separators: convert backslashes to forward slashes first (for Windows-created archives)
	// then use path.Clean for validation
	filePath := strings.ReplaceAll(fileName, "\\", "/")
	filePath = path.Clean(filePath)

	// Check for path traversal attack (zip slip)
	if strings.HasPrefix(filePath, "../") || strings.HasPrefix(filePath, "/") || strings.Contains(filePath, "/../") {
		return fmt.Errorf("invalid file path in archive: %s", f.Name)
	}

	// Convert to OS-specific path separators for file system operations
	targetPath := filepath.Join(destination, filepath.FromSlash(filePath))

	if f.FileInfo().IsDir() {
		return afs.MkdirAll(targetPath, dirMode)
	}

	// Create parent directory
	if err := afs.MkdirAll(filepath.Dir(targetPath), dirMode); err != nil {
		return err
	}

	// Open source file in archive
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("failed to open file in archive: %w", err)
	}
	defer rc.Close()

	// Create destination file
	outFile, err := afs.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fileMode)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer outFile.Close()

	if _, err := copyWithCacheControl(outFile, rc, afs, targetPath, progress, sourceDrop); err != nil {
		return fmt.Errorf("failed to extract file: %w", err)
	}
	progress.finishFile()

	return nil
}

// extractTarGz extracts a .tar.gz archive using streaming
func extractTarGz(afs afero.Fs, archivePath, destination string, fileMode, dirMode os.FileMode, start time.Time) error {
	// Get the real path
	realPath := archivePath
	if bpfs, ok := afs.(*afero.BasePathFs); ok {
		realPath = afero.FullBaseFsPath(bpfs, archivePath)
	}

	// Open the gzip file
	file, err := os.Open(realPath)
	if err != nil {
		return fmt.Errorf("failed to open archive: %w", err)
	}
	defer file.Close()
	adviseFileSequential(file)

	// Create gzip reader
	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzReader.Close()

	dropArchiveCache := func() {
		adviseFileDontNeed(file, 0, 0)
	}
	err = extractTarReader(afs, gzReader, archivePath, destination, fileMode, dirMode, start, dropArchiveCache)
	adviseFileDontNeed(file, 0, 0)
	return err
}

// extractTar extracts a .tar archive using streaming
func extractTar(afs afero.Fs, archivePath, destination string, fileMode, dirMode os.FileMode, start time.Time) error {
	// Get the real path
	realPath := archivePath
	if bpfs, ok := afs.(*afero.BasePathFs); ok {
		realPath = afero.FullBaseFsPath(bpfs, archivePath)
	}

	// Open the tar file
	file, err := os.Open(realPath)
	if err != nil {
		return fmt.Errorf("failed to open archive: %w", err)
	}
	defer file.Close()
	adviseFileSequential(file)

	dropArchiveCache := func() {
		adviseFileDontNeed(file, 0, 0)
	}
	err = extractTarReader(afs, file, archivePath, destination, fileMode, dirMode, start, dropArchiveCache)
	adviseFileDontNeed(file, 0, 0)
	return err
}

// extractTarReader extracts from a tar reader (used by both tar and tar.gz)
func extractTarReader(afs afero.Fs, reader io.Reader, archivePath, destination string, fileMode, dirMode os.FileMode, start time.Time, sourceDrop func()) error {
	tarReader := tar.NewReader(reader)
	progress := newExtractProgress(archivePath, destination, start, 0, 0)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar header: %w", err)
		}

		// Normalize path separators: convert backslashes to forward slashes first (for Windows-created archives)
		// then use path.Clean for validation
		filePath := strings.ReplaceAll(header.Name, "\\", "/")
		filePath = path.Clean(filePath)

		// Check for path traversal attack
		if strings.HasPrefix(filePath, "../") || strings.HasPrefix(filePath, "/") || strings.Contains(filePath, "/../") {
			return fmt.Errorf("invalid file path in archive: %s", header.Name)
		}

		// Convert to OS-specific path separators for file system operations
		targetPath := filepath.Join(destination, filepath.FromSlash(filePath))

		switch header.Typeflag {
		case tar.TypeDir:
			if err := afs.MkdirAll(targetPath, dirMode); err != nil {
				return fmt.Errorf("failed to create directory: %w", err)
			}

		case tar.TypeReg:
			// Create parent directory
			if err := afs.MkdirAll(filepath.Dir(targetPath), dirMode); err != nil {
				return err
			}

			// Create file
			outFile, err := afs.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fileMode)
			if err != nil {
				return fmt.Errorf("failed to create file: %w", err)
			}

			_, err = copyWithCacheControl(outFile, tarReader, afs, targetPath, progress, sourceDrop)
			outFile.Close()
			if err != nil {
				return fmt.Errorf("failed to extract file: %w", err)
			}
			progress.finishFile()

		case tar.TypeSymlink:
			// Skip symlinks for security
			continue

		case tar.TypeLink:
			// Skip hard links for security
			continue

		default:
			// Skip unknown types
			continue
		}
	}

	return nil
}

type cacheDroppingWriter struct {
	file     afero.File
	afs      afero.Fs
	path     string
	progress *extractProgress
	source   func()
	written  int64
	nextDrop int64
}

func (w *cacheDroppingWriter) Write(p []byte) (int, error) {
	n, err := w.file.Write(p)
	if n > 0 {
		w.written += int64(n)
		w.progress.addBytes(n)
		if w.written >= w.nextDrop {
			w.flushAndDrop()
			w.nextDrop = w.written + extractCacheDropEvery
		}
	}
	return n, err
}

func (w *cacheDroppingWriter) flushAndDrop() {
	_ = w.file.Sync()
	if realPath, ok := realFsPath(w.afs, w.path); ok {
		advisePathDontNeed(realPath)
	}
	if w.source != nil {
		w.source()
	}
}

func copyWithCacheControl(dst afero.File, src io.Reader, afs afero.Fs, targetPath string, progress *extractProgress, sourceDrop func()) (int64, error) {
	writer := &cacheDroppingWriter{
		file:     dst,
		afs:      afs,
		path:     targetPath,
		progress: progress,
		source:   sourceDrop,
		nextDrop: extractCacheDropEvery,
	}
	buf := make([]byte, extractCopyBufferSize)
	written, err := io.CopyBuffer(writer, src, buf)
	writer.flushAndDrop()
	return written, err
}

func realFsPath(afs afero.Fs, filePath string) (string, bool) {
	if bpfs, ok := afs.(*afero.BasePathFs); ok {
		return afero.FullBaseFsPath(bpfs, filePath), true
	}

	switch afs.(type) {
	case afero.OsFs, *afero.OsFs:
		return filePath, true
	}

	return "", false
}
