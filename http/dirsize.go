package fbhttp

import (
	"context"
	"net/http"
	"os"
	"path"

	"github.com/spf13/afero"

	"github.com/filebrowser/filebrowser/v2/files"
)

type dirSizeResponse struct {
	Size     int64 `json:"size"`
	NumFiles int64 `json:"numFiles"`
	NumDirs  int64 `json:"numDirs"`
}

var dirSizeHandler = withUser(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
	info, err := files.NewFileInfo(&files.FileOptions{
		Fs:         d.user.Fs,
		Path:       r.URL.Path,
		Modify:     d.user.Perm.Modify,
		Expand:     false,
		ReadHeader: false,
		Checker:    d,
		Content:    false,
	})
	if err != nil {
		return errToStatus(err), err
	}

	if !info.IsDir {
		return http.StatusBadRequest, nil
	}

	size, numFiles, numDirs, err := computeDirSize(r.Context(), d.user.Fs, info.Path, d)
	if err != nil {
		if errorsIsCanceled(err) {
			return 0, err
		}
		return errToStatus(err), err
	}

	return renderJSON(w, r, dirSizeResponse{
		Size:     size,
		NumFiles: numFiles,
		NumDirs:  numDirs,
	})
})

func computeDirSize(ctx context.Context, fs afero.Fs, root string, checker interface{ Check(string) bool }) (size, numFiles, numDirs int64, err error) {
	root = path.Clean(path.Join("/", root))
	if root != "/" && !checker.Check(root) {
		return 0, 0, 0, os.ErrPermission
	}

	type dirItem struct {
		path string
	}

	stack := []dirItem{{path: root}}
	for len(stack) > 0 {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, 0, 0, ctxErr
		}

		dir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		entries, readErr := afero.ReadDir(fs, dir.path)
		if readErr != nil {
			return 0, 0, 0, readErr
		}

		for _, entry := range entries {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return 0, 0, 0, ctxErr
			}

			child := path.Join(dir.path, entry.Name())
			if !checker.Check(child) {
				continue
			}

			if entry.IsDir() {
				numDirs++
				if isSymlinkDir(fs, child, entry) {
					continue
				}
				stack = append(stack, dirItem{path: child})
				continue
			}

			numFiles++
			size += entry.Size()
		}
	}

	return size, numFiles, numDirs, nil
}

func isSymlinkDir(fs afero.Fs, filePath string, info os.FileInfo) bool {
	if !info.IsDir() {
		return false
	}

	lstater, ok := fs.(afero.Lstater)
	if !ok {
		return false
	}

	linfo, _, err := lstater.LstatIfPossible(filePath)
	if err != nil {
		return false
	}

	return (linfo.Mode() & os.ModeSymlink) != 0
}

func errorsIsCanceled(err error) bool {
	return err == context.Canceled || err == context.DeadlineExceeded
}
