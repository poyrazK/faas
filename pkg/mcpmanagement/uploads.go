package mcpmanagement

import (
	"archive/tar"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Remote agents deploy code they wrote (ADR-20261026195952). A one-time
// upload URL or inline files land in a private directory the hosted bridge
// owns, sanitized, and the same CLI deploy path as a local directory runs on
// it. Nothing an upload contains can name a path outside its directory, link
// elsewhere, or carry git configuration.
const (
	uploadTTL            = 15 * time.Minute
	uploadDirTTL         = 30 * time.Minute
	uploadReadDeadline   = 10 * time.Minute
	maxUploadsPerAccount = 2
	maxUploads           = 200
	maxUploadEntries     = 10_000
	maxInlineFiles       = 200
	maxInlineBytes       = 96 << 10
	maxUploadPath        = 1024
)

// uploadStore holds pending uploads and the directories plans deploy from.
type uploadStore struct {
	mu      sync.Mutex
	root    string
	base    string
	uploads map[string]*sourceUpload
	tokens  map[string]string
	dirs    map[string]time.Time
}

type sourceUpload struct {
	id, grantID, accountID, app string
	maxBytes                    int64
	expires                     time.Time
	receiving, received, taken  bool
	size                        int64
}

// newUploadStore empties root, which only the bridge uses, and serves upload
// URLs under resourceURL + "/uploads/".
func newUploadStore(root, resourceURL string) (*uploadStore, error) {
	if root == "" || !filepath.IsAbs(root) {
		return nil, errors.New("the upload directory must be an absolute path")
	}
	if err := os.RemoveAll(root); err != nil {
		return nil, fmt.Errorf("reset upload directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "plans"), 0o700); err != nil {
		return nil, fmt.Errorf("create upload directory: %w", err)
	}
	return &uploadStore{root: root, base: resourceURL + "/uploads/", uploads: map[string]*sourceUpload{}, tokens: map[string]string{}, dirs: map[string]time.Time{}}, nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (u *uploadStore) tarball(id string) string { return filepath.Join(u.root, id+".tar.gz") }

// pruneLocked forgets expired uploads and removes directories past their use.
func (u *uploadStore) pruneLocked() {
	now := time.Now()
	for id, up := range u.uploads {
		if now.After(up.expires) && !up.receiving {
			_ = os.Remove(u.tarball(id))
			delete(u.uploads, id)
		}
	}
	for token, id := range u.tokens {
		if u.uploads[id] == nil {
			delete(u.tokens, token)
		}
	}
	for dir, expires := range u.dirs {
		if now.After(expires) {
			_ = os.RemoveAll(dir)
			delete(u.dirs, dir)
		}
	}
}

// create reserves a one-time upload for one grant and app.
func (u *uploadStore) create(grantID, accountID, app string, maxBytes int64) (*sourceUpload, string, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.pruneLocked()
	pending := 0
	for _, up := range u.uploads {
		if up.accountID == accountID && !up.taken {
			pending++
		}
	}
	if pending >= maxUploadsPerAccount || len(u.uploads) >= maxUploads {
		return nil, "", invalid("upload_capacity", fmt.Sprintf("at most %d uploads may wait per account; deploy or let one expire", maxUploadsPerAccount))
	}
	id, err := randomHex(16)
	if err != nil {
		return nil, "", err
	}
	token, err := randomHex(32)
	if err != nil {
		return nil, "", err
	}
	up := &sourceUpload{id: id, grantID: grantID, accountID: accountID, app: app, maxBytes: maxBytes, expires: time.Now().Add(uploadTTL)}
	u.uploads[id], u.tokens[tokenHash(token)] = up, id
	return up, u.base + token, nil
}

// receive stores the body of PUT /mcp/uploads/{token} once.
func (u *uploadStore) receive(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPut && req.Method != http.MethodPost {
		w.Header().Set("Allow", "PUT, POST")
		http.Error(w, "Use PUT", http.StatusMethodNotAllowed)
		return
	}
	token := strings.TrimPrefix(req.URL.Path, "/mcp/uploads/")
	u.mu.Lock()
	u.pruneLocked()
	up := u.uploads[u.tokens[tokenHash(token)]]
	if up == nil || len(token) != 64 || up.received || up.receiving {
		u.mu.Unlock()
		writeOAuthJSON(w, http.StatusNotFound, map[string]string{"error": "upload_unavailable", "error_description": "the upload URL expired, was used, or is unknown; create a new one"})
		return
	}
	up.receiving = true
	u.mu.Unlock()

	// The server's short read and write timeouts cover MCP frames, not an
	// archive: extend both for this request.
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(uploadReadDeadline))
	_ = controller.SetWriteDeadline(time.Now().Add(uploadReadDeadline + time.Minute))
	size, err := u.store(up, http.MaxBytesReader(w, req.Body, up.maxBytes))

	u.mu.Lock()
	defer u.mu.Unlock()
	up.receiving = false
	if err != nil {
		_ = os.Remove(u.tarball(up.id))
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeOAuthJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "upload_too_large", "error_description": fmt.Sprintf("the source is larger than this plan's %d-byte limit", up.maxBytes)})
			return
		}
		writeOAuthJSON(w, http.StatusBadRequest, map[string]string{"error": "upload_failed", "error_description": "the upload did not complete; retry with the same URL"})
		return
	}
	up.received, up.size = true, size
	writeOAuthJSON(w, http.StatusCreated, map[string]any{"upload_id": up.id, "bytes": size, "next": "Call gregale_plan_upload_deploy with this upload_id."})
}

func (u *uploadStore) store(up *sourceUpload, body io.Reader) (int64, error) {
	f, err := os.OpenFile(u.tarball(up.id), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:forbidigo // bridge-owned private upload directory
	if err != nil {
		return 0, err
	}
	size, err := io.Copy(f, body)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil && size == 0 {
		err = errors.New("empty upload")
	}
	return size, err
}

// take hands a received upload to one plan, once.
func (u *uploadStore) take(grantID, id string) (*sourceUpload, string, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.pruneLocked()
	up := u.uploads[id]
	if up == nil || up.grantID != grantID || up.taken {
		return nil, "", invalid("unknown_upload", "the upload is unknown, expired, already planned, or belongs to another connection")
	}
	if !up.received {
		return nil, "", invalid("upload_pending", "nothing was uploaded to this URL yet; upload the archive first")
	}
	up.taken = true
	return up, u.tarball(id), nil
}

// newDir is a private directory a plan deploys from, removed after use.
func (u *uploadStore) newDir() (string, error) {
	id, err := randomHex(16)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(u.root, "plans", id)
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", err
	}
	u.mu.Lock()
	u.dirs[dir] = time.Now().Add(uploadDirTTL)
	u.mu.Unlock()
	return dir, nil
}

func (u *uploadStore) removeDir(dir string) {
	u.mu.Lock()
	delete(u.dirs, dir)
	u.mu.Unlock()
	_ = os.RemoveAll(dir)
}

// uploadPath validates one archive or inline path: relative, no empty, "."
// or ".." components, no backslashes or NUL, nothing named .git at any depth,
// and no linked project context. It returns the path under dir.
func uploadPath(dir, name string) (string, error) {
	name = strings.TrimPrefix(name, "./")
	trimmed := strings.TrimSuffix(name, "/")
	if trimmed == "" || len(name) > maxUploadPath || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\\\x00") {
		return "", fmt.Errorf("invalid path %q", name)
	}
	parts := strings.Split(trimmed, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.EqualFold(part, ".git") {
			return "", fmt.Errorf("path %q is not allowed", name)
		}
	}
	if len(parts) == 2 && strings.EqualFold(parts[0], ".gregale") && strings.EqualFold(parts[1], "project.json") {
		return "", fmt.Errorf("linked project context %q is not allowed; deploy the project with the CLI", name)
	}
	return filepath.Join(append([]string{dir}, parts...)...), nil
}

// writeUploadFile creates one regular file; a repeated path is refused.
func writeUploadFile(target string, executable bool, content io.Reader, limit int64) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return 0, err
	}
	mode := os.FileMode(0o600)
	if executable {
		mode = 0o700
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode) //nolint:forbidigo // sanitized path under the bridge-owned plan directory
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return 0, fmt.Errorf("path %q appears twice", filepath.Base(target))
		}
		return 0, err
	}
	n, err := io.Copy(f, io.LimitReader(content, limit+1))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil && n > limit {
		err = errors.New("the unpacked source exceeds this plan's source limit")
	}
	return n, err
}

// extractUpload unpacks a .tar.gz into dir: only regular files and
// directories, at most maxUploadEntries entries and limit unpacked bytes.
func extractUpload(archive, dir string, limit int64) error {
	f, err := os.Open(archive) //nolint:forbidigo // bridge-owned private upload file
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return errors.New("the upload is not a gzip-compressed tar archive")
	}
	tr := tar.NewReader(gz)
	var entries int
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return errors.New("the upload is not a valid tar archive")
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		if entries++; entries > maxUploadEntries {
			return fmt.Errorf("the upload has more than %d entries", maxUploadEntries)
		}
		if h.Name == "./" || h.Name == "." {
			continue
		}
		target, err := uploadPath(dir, h.Name)
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			n, err := writeUploadFile(target, h.Mode&0o111 != 0, tr, limit-total)
			if err != nil {
				return err
			}
			total += n
		default:
			return fmt.Errorf("%q is not a regular file or directory; uploads may not contain links or devices", h.Name)
		}
	}
	if entries == 0 {
		return errors.New("the upload is empty")
	}
	return nil
}

// inlineFile is one text file passed in the tool call.
type inlineFile struct {
	Path       string `json:"path" jsonschema:"Relative path, for example src/index.js"`
	Content    string `json:"content" jsonschema:"The file's text"`
	Executable bool   `json:"executable,omitempty" jsonschema:"Mark the file executable"`
}

func writeInlineFiles(dir string, files []inlineFile) error {
	if len(files) == 0 || len(files) > maxInlineFiles {
		return fmt.Errorf("pass 1 to %d files", maxInlineFiles)
	}
	var total int
	for _, file := range files {
		total += len(file.Content)
	}
	if total > maxInlineBytes {
		return fmt.Errorf("inline files total %d bytes; the limit is %d. Use gregale_create_source_upload", total, maxInlineBytes)
	}
	for _, file := range files {
		target, err := uploadPath(dir, file.Path)
		if err != nil {
			return err
		}
		if _, err := writeUploadFile(target, file.Executable, strings.NewReader(file.Content), maxInlineBytes); err != nil {
			return err
		}
	}
	return nil
}
