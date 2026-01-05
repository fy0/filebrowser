package fbhttp

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/spf13/afero"
	"golang.org/x/text/encoding/simplifiedchinese"

	"github.com/filebrowser/filebrowser/v2/files"
)

// extractHandler handles archive extraction requests
// POST /api/extract/{path}?destination=...&mode=...
// mode: "here" (extract to same directory) or "subdir" (extract to subdirectory named after archive)
var extractHandler = withUser(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
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

	// Determine archive type and extract
	lowerPath := strings.ToLower(archivePath)
	switch {
	case strings.HasSuffix(lowerPath, ".zip"):
		err = extractZip(d.user.Fs, archivePath, destination, d.settings.FileMode, d.settings.DirMode)
	case strings.HasSuffix(lowerPath, ".tar.gz") || strings.HasSuffix(lowerPath, ".tgz"):
		err = extractTarGz(d.user.Fs, archivePath, destination, d.settings.FileMode, d.settings.DirMode)
	case strings.HasSuffix(lowerPath, ".tar"):
		err = extractTar(d.user.Fs, archivePath, destination, d.settings.FileMode, d.settings.DirMode)
	default:
		return http.StatusBadRequest, errors.New("unsupported archive format: only .zip, .tar.gz, .tgz, and .tar are supported")
	}

	if err != nil {
		return errToStatus(err), err
	}

	return http.StatusOK, nil
})

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
func extractZip(afs afero.Fs, archivePath, destination string, fileMode, dirMode os.FileMode) error {
	// Get the real path for zip.OpenReader
	realPath := archivePath
	if bpfs, ok := afs.(*afero.BasePathFs); ok {
		realPath = afero.FullBaseFsPath(bpfs, archivePath)
	}

	// Open the zip file
	zipReader, err := zip.OpenReader(realPath)
	if err != nil {
		return fmt.Errorf("failed to open zip file: %w", err)
	}
	defer zipReader.Close()

	for _, f := range zipReader.File {
		if err := extractZipFile(afs, f, destination, fileMode, dirMode); err != nil {
			return err
		}
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
func extractZipFile(afs afero.Fs, f *zip.File, destination string, fileMode, dirMode os.FileMode) error {
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

	// Stream copy with limited buffer (8KB) to minimize memory usage
	buf := make([]byte, 8*1024)
	_, err = io.CopyBuffer(outFile, rc, buf)
	if err != nil {
		return fmt.Errorf("failed to extract file: %w", err)
	}

	return nil
}

// extractTarGz extracts a .tar.gz archive using streaming
func extractTarGz(afs afero.Fs, archivePath, destination string, fileMode, dirMode os.FileMode) error {
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

	// Create gzip reader
	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzReader.Close()

	return extractTarReader(afs, gzReader, destination, fileMode, dirMode)
}

// extractTar extracts a .tar archive using streaming
func extractTar(afs afero.Fs, archivePath, destination string, fileMode, dirMode os.FileMode) error {
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

	return extractTarReader(afs, file, destination, fileMode, dirMode)
}

// extractTarReader extracts from a tar reader (used by both tar and tar.gz)
func extractTarReader(afs afero.Fs, reader io.Reader, destination string, fileMode, dirMode os.FileMode) error {
	tarReader := tar.NewReader(reader)

	// Use a buffer for streaming extraction
	buf := make([]byte, 8*1024)

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

			// Stream copy
			_, err = io.CopyBuffer(outFile, tarReader, buf)
			outFile.Close()
			if err != nil {
				return fmt.Errorf("failed to extract file: %w", err)
			}

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

// getSupportedArchiveFormats returns a list of supported archive formats
func getSupportedArchiveFormats() []string {
	return []string{".zip", ".tar.gz", ".tgz", ".tar"}
}

// isArchiveFile checks if a file is a supported archive
func isArchiveFile(filename string) bool {
	lower := strings.ToLower(filename)
	for _, ext := range getSupportedArchiveFormats() {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}
