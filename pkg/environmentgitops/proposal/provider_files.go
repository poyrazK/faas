package proposal

import (
	"bytes"
	"context"
	"crypto/sha1" // #nosec G505 -- Git object IDs use SHA-1; authorization uses SHA-256.
	"encoding/base64"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func gitBlob(raw []byte) string {
	h := sha1.New() //nolint:gosec // Required Git object identity; plan uses SHA-256.
	_, _ = fmt.Fprintf(h, "blob %d\x00", len(raw))
	_, _ = h.Write(raw)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func decodeBlob(content, blob string) ([]byte, error) {
	if len(content) > 2*api.EnvironmentGitOpsMaxDefinitionBytes {
		return nil, fmt.Errorf("provider manifest is too large")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(content, "\n", ""))
	if err != nil || len(raw) == 0 || len(raw) > api.EnvironmentGitOpsMaxDefinitionBytes || gitBlob(raw) != blob {
		return nil, fmt.Errorf("provider manifest content identity mismatch")
	}
	return raw, nil
}

func (c Client) File(ctx context.Context, commit, manifest string) (File, error) {
	if c.Provider == "gitlab" {
		return c.gitlabFile(ctx, commit, manifest)
	}
	var out File
	var parent struct {
		SHA  string `json:"sha"`
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if err := c.Call(ctx, "GET", c.endpoint()+"/git/commits/"+commit, nil, &parent); err != nil {
		return out, err
	}
	if parent.SHA != commit || !validHex(parent.Tree.SHA, 40) {
		return out, fmt.Errorf("invalid provider commit tree")
	}
	out.Tree = parent.Tree.SHA
	tree := out.Tree
	parts := strings.Split(manifest, "/")
	for i, part := range parts {
		var listing struct {
			SHA       string                                   `json:"sha"`
			Truncated bool                                     `json:"truncated"`
			Tree      []struct{ Path, Mode, Type, SHA string } `json:"tree"`
		}
		if err := c.Call(ctx, "GET", c.endpoint()+"/git/trees/"+tree, nil, &listing); err != nil {
			return out, err
		}
		if listing.Truncated || listing.SHA != tree {
			return out, fmt.Errorf("provider tree is incomplete")
		}
		found := false
		for _, entry := range listing.Tree {
			if entry.Path != part {
				continue
			}
			if !validHex(entry.SHA, 40) {
				return out, fmt.Errorf("invalid provider tree entry")
			}
			if i < len(parts)-1 {
				if entry.Type != "tree" || entry.Mode != "040000" {
					return out, fmt.Errorf("manifest ancestor is not a directory")
				}
				tree = entry.SHA
			} else {
				if entry.Type != "blob" || entry.Mode != "100644" {
					return out, fmt.Errorf("manifest must be a regular non-executable file")
				}
				out.Blob, out.Mode = entry.SHA, entry.Mode
			}
			found = true
			break
		}
		if !found {
			return out, ErrNotFound
		}
	}
	var blob struct{ SHA, Encoding, Content string }
	if err := c.Call(ctx, "GET", c.endpoint()+"/git/blobs/"+out.Blob, nil, &blob); err != nil {
		return out, err
	}
	if blob.SHA != out.Blob || blob.Encoding != "base64" {
		return out, fmt.Errorf("provider blob identity mismatch")
	}
	var err error
	out.Content, err = decodeBlob(blob.Content, out.Blob)
	return out, err
}

func (c Client) gitlabFile(ctx context.Context, commit, manifest string) (File, error) {
	var out File
	var file struct {
		Path       string `json:"file_path"`
		Blob       string `json:"blob_id"`
		Content    string `json:"content"`
		Encoding   string `json:"encoding"`
		Commit     string `json:"commit_id"`
		LastCommit string `json:"last_commit_id"`
	}
	if err := c.Call(ctx, "GET", c.endpoint()+"/repository/files/"+url.PathEscape(manifest)+"?ref="+commit, nil, &file); err != nil {
		return out, err
	}
	if file.Path != manifest || file.Commit != commit || !validHex(file.LastCommit, 40) || file.Encoding != "base64" {
		return out, fmt.Errorf("provider file identity mismatch")
	}
	// The files API can describe symlinks; inspect its actual immutable tree mode.
	directory := path.Dir(manifest)
	if directory == "." {
		directory = ""
	}
	for page := 1; page <= 100; page++ {
		var entries []struct{ ID, Path, Mode, Type string }
		endpoint := c.endpoint() + "/repository/tree?ref=" + commit + "&path=" + url.QueryEscape(directory) + "&per_page=100&page=" + strconv.Itoa(page)
		if err := c.Call(ctx, "GET", endpoint, nil, &entries); err != nil {
			return out, err
		}
		for _, entry := range entries {
			if entry.Path != manifest {
				continue
			}
			if entry.Type != "blob" || entry.Mode != "100644" || entry.ID != file.Blob {
				return out, fmt.Errorf("manifest must be a regular non-executable file")
			}
			out.Blob, out.Mode, out.LastCommit = file.Blob, entry.Mode, file.LastCommit
			var err error
			out.Content, err = decodeBlob(file.Content, file.Blob)
			return out, err
		}
		if len(entries) < 100 {
			break
		}
	}
	return out, fmt.Errorf("manifest tree could not be fully verified")
}

func (c Client) Verify(ctx context.Context, head, base, manifest string, expected File) error {
	if !validHex(head, 40) || head == base {
		return fmt.Errorf("invalid proposal commit")
	}
	if c.Provider == "gitlab" {
		var commit struct {
			ID      string   `json:"id"`
			Parents []string `json:"parent_ids"`
		}
		if err := c.Call(ctx, "GET", c.endpoint()+"/repository/commits/"+head, nil, &commit); err != nil {
			return err
		}
		if commit.ID != head || len(commit.Parents) != 1 || commit.Parents[0] != base {
			return fmt.Errorf("proposal has unexpected commit ancestry")
		}
		var diffs []struct {
			OldPath   string `json:"old_path"`
			NewPath   string `json:"new_path"`
			AMode     string `json:"a_mode"`
			BMode     string `json:"b_mode"`
			NewFile   bool   `json:"new_file"`
			Renamed   bool   `json:"renamed_file"`
			Deleted   bool   `json:"deleted_file"`
			Diff      string `json:"diff"`
			Collapsed bool   `json:"collapsed"`
			TooLarge  bool   `json:"too_large"`
		}
		if err := c.Call(ctx, "GET", c.endpoint()+"/repository/commits/"+head+"/diff?per_page=2", nil, &diffs); err != nil {
			return err
		}
		if len(diffs) != 1 || diffs[0].OldPath != manifest || diffs[0].NewPath != manifest || diffs[0].AMode != expected.Mode || diffs[0].BMode != expected.Mode || diffs[0].NewFile || diffs[0].Renamed || diffs[0].Deleted || diffs[0].Diff == "" || diffs[0].Collapsed || diffs[0].TooLarge {
			return fmt.Errorf("proposal changes unexpected files or file modes")
		}
	} else {
		var commit struct {
			SHA     string
			Parents []struct{ SHA string }
			Files   []struct {
				Filename, Status, SHA string
				Previous              string `json:"previous_filename"`
			}
		}
		if err := c.Call(ctx, "GET", c.endpoint()+"/commits/"+head+"?per_page=2", nil, &commit); err != nil {
			return err
		}
		if commit.SHA != head || len(commit.Parents) != 1 || commit.Parents[0].SHA != base {
			return fmt.Errorf("proposal has unexpected commit ancestry")
		}
		if len(commit.Files) != 1 || commit.Files[0].Filename != manifest || commit.Files[0].Status != "modified" || commit.Files[0].Previous != "" || commit.Files[0].SHA != gitBlob(expected.Content) {
			return fmt.Errorf("proposal changes unexpected files")
		}
	}
	actual, err := c.File(ctx, head, manifest)
	if err != nil {
		return err
	}
	if actual.Mode != expected.Mode || !bytes.Equal(actual.Content, expected.Content) {
		return fmt.Errorf("proposal manifest was changed")
	}
	return nil
}

func (c Client) Commit(ctx context.Context, base, branch, manifest string, file File, title string) (string, error) {
	if c.Provider == "gitlab" {
		var out struct {
			ID string `json:"id"`
		}
		err := c.Call(ctx, "POST", c.endpoint()+"/repository/commits", map[string]any{"branch": branch, "start_sha": base, "force": false, "commit_message": title,
			"actions": []map[string]any{{"action": "update", "file_path": manifest, "content": base64.StdEncoding.EncodeToString(file.Content), "encoding": "base64", "last_commit_id": file.LastCommit}}}, &out)
		if err != nil {
			return "", err
		}
		if !validHex(out.ID, 40) {
			return "", fmt.Errorf("invalid created proposal commit")
		}
		return out.ID, nil
	}
	var tree struct{ SHA string }
	err := c.Call(ctx, "POST", c.endpoint()+"/git/trees", map[string]any{"base_tree": file.Tree, "tree": []map[string]any{{"path": manifest, "mode": file.Mode, "type": "blob", "content": string(file.Content)}}}, &tree)
	if err != nil {
		return "", err
	}
	if !validHex(tree.SHA, 40) {
		return "", fmt.Errorf("invalid created proposal tree")
	}
	var commit struct{ SHA string }
	err = c.Call(ctx, "POST", c.endpoint()+"/git/commits", map[string]any{"message": title, "tree": tree.SHA, "parents": []string{base}}, &commit)
	if err != nil {
		return "", err
	}
	if !validHex(commit.SHA, 40) {
		return "", fmt.Errorf("invalid created proposal commit")
	}
	// Creation is exclusive. There is no update-ref or force-push path.
	var ref any
	err = c.Call(ctx, "POST", c.endpoint()+"/git/refs", map[string]any{"ref": "refs/heads/" + branch, "sha": commit.SHA}, &ref)
	return commit.SHA, err
}
