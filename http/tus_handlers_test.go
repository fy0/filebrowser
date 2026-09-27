package fbhttp

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/spf13/afero"

	"github.com/filebrowser/filebrowser/v2/runner"
	"github.com/filebrowser/filebrowser/v2/settings"
	"github.com/filebrowser/filebrowser/v2/storage"
	"github.com/filebrowser/filebrowser/v2/users"
)

type uploadTestFs struct {
	afero.Fs
	syncErr  error
	closeErr error
	syncs    int
}

func (fs *uploadTestFs) OpenFile(name string, flag int, mode os.FileMode) (afero.File, error) {
	file, err := fs.Fs.OpenFile(name, flag, mode)
	if err != nil {
		return nil, err
	}
	return &uploadTestFile{File: file, fs: fs}, nil
}

type uploadTestFile struct {
	afero.File
	fs *uploadTestFs
}

func (f *uploadTestFile) Sync() error {
	f.fs.syncs++
	return f.fs.syncErr
}

func (f *uploadTestFile) Close() error {
	return errors.Join(f.File.Close(), f.fs.closeErr)
}

type uploadTestUsers struct {
	users.Store
	user *users.User
}

func (s uploadTestUsers) Get(_ string, _ interface{}) (*users.User, error) { return s.user, nil }
func (uploadTestUsers) LastUpdate(_ uint) int64                            { return 0 }

func TestTusPatchCacheControl(t *testing.T) {
	failure := errors.New("writeback failed")
	for _, tc := range []struct {
		name      string
		remaining int64
		syncErr   error
		closeErr  error
	}{
		{name: "complete"},
		{name: "partial", remaining: 1},
		{name: "sync failure", syncErr: failure},
		{name: "close failure", closeErr: failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := afero.NewMemMapFs()
			const name = "backup.zip"
			fullName := filepath.Join("scope", name)
			if err := base.MkdirAll("scope", 0o700); err != nil {
				t.Fatal(err)
			}
			prefix := []byte("existing chunk")
			payload := bytes.Repeat([]byte("next chunk"), 10000)
			if err := afero.WriteFile(base, fullName, prefix, 0o600); err != nil {
				t.Fatal(err)
			}
			fs := &uploadTestFs{Fs: base, syncErr: tc.syncErr, closeErr: tc.closeErr}
			user := &users.User{ID: 1, Fs: afero.NewBasePathFs(fs, "scope"), Perm: users.Permissions{Create: true}}
			cfg := &settings.Settings{Key: []byte("test-signing-key"), FileMode: 0o600}
			d := &data{
				Runner:   &runner.Runner{},
				settings: cfg,
				server:   &settings.Server{},
				store:    &storage.Storage{Users: uploadTestUsers{user: user}},
			}
			token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, authToken{
				User:             userInfo{ID: 1},
				RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(2 * time.Hour))},
			}).SignedString(cfg.Key)
			if err != nil {
				t.Fatal(err)
			}
			total := int64(len(prefix)+len(payload)) + tc.remaining
			registerUpload(fullName, total)
			t.Cleanup(func() { completeUpload(fullName) })
			req := httptest.NewRequest(http.MethodPatch, "/backup.zip", bytes.NewReader(payload))
			req.URL.Path = name
			req.Header.Set("X-Auth", token)
			req.Header.Set("Content-Type", "application/offset+octet-stream")
			req.Header.Set("Upload-Offset", strconv.Itoa(len(prefix)))
			response := httptest.NewRecorder()
			status, err := tusPatchHandler()(response, req, d)
			_, activeErr := getActiveUploadLength(fullName)
			if tc.syncErr != nil || tc.closeErr != nil {
				if status != http.StatusInternalServerError || !errors.Is(err, failure) || activeErr != nil {
					t.Fatalf("failed upload: status=%d error=%v active=%v", status, err, activeErr)
				}
				if response.Header().Get("Upload-Offset") != "" {
					t.Fatal("failed write acknowledged an upload offset")
				}
				return
			}
			if status != http.StatusNoContent || err != nil || fs.syncs == 0 {
				t.Fatalf("upload: status=%d error=%v syncs=%d", status, err, fs.syncs)
			}
			if got := response.Header().Get("Upload-Offset"); got != strconv.Itoa(len(prefix)+len(payload)) {
				t.Fatalf("offset = %s", got)
			}
			if (activeErr == nil) != (tc.remaining > 0) {
				t.Fatalf("unexpected upload completion: %v", activeErr)
			}
			got, err := afero.ReadFile(base, fullName)
			if err != nil || !bytes.Equal(got, append(prefix, payload...)) {
				t.Fatalf("appended contents differ: %v", err)
			}
		})
	}
}

func TestWriteFileWritebackErrors(t *testing.T) {
	failure := errors.New("writeback failed")
	for _, fs := range []*uploadTestFs{
		{Fs: afero.NewMemMapFs(), syncErr: failure},
		{Fs: afero.NewMemMapFs(), closeErr: failure},
	} {
		_, err := writeFile(fs, "backup.zip", bytes.NewBufferString("backup"), 0o600, 0o700)
		if !errors.Is(err, failure) {
			t.Fatalf("upload error = %v, want %v", err, failure)
		}
	}
}
