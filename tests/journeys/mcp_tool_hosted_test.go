package journeys

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func (j *mcpToolJourney) scaffoldHosted(t *testing.T) {
	minted := object(t, j.tg.ok(t, time.Minute, "keys", "add", j.tg.slug("publisher"), "--scopes", "agent,deploy:write").stdout)
	keyID := str(minted, "id")
	j.publisherKey = str(minted, "plaintext")
	if keyID == "" || j.publisherKey == "" {
		t.Fatal("publishing key mint omitted its identity")
	}
	j.root.Cleanup(func() { j.tg.run(j.root, time.Minute, "key", "delete", keyID) })
	j.owner = j.connectHostedKey(t, "2026-07-28", nil, j.publisherKey)
	j.requireTools(t, j.owner, "gregale_get_tool_template", "gregale_create_source_upload", "gregale_plan_uploaded_tool_deploy", "gregale_apply_tool_deploy", "gregale_get_tool_deploy")
	manifest := j.call(t, j.owner, "gregale_get_tool_template", map[string]any{"runtime": "node"}, false)
	files, ok := manifest["files"].([]any)
	if !ok || len(files) == 0 || len(files) > 200 || len(str(manifest, "template_sha256")) != 64 {
		t.Fatal("invalid hosted starter manifest")
	}
	j.path = filepath.Join(j.root.TempDir(), "tool")
	var total int
	for _, entry := range files {
		name, ok := entry.(string)
		if !ok || !fs.ValidPath(name) || name == "." || strings.Contains(name, "\\") {
			t.Fatal("invalid hosted template path")
		}
		var content []byte
		for offset, pages := 0, 0; ; pages++ {
			if pages >= 128 {
				t.Fatal("starter file exceeded bounded pagination")
			}
			chunk := j.call(t, j.owner, "gregale_get_tool_template", map[string]any{"runtime": "node", "file": name, "offset": offset}, false)
			data, err := base64.StdEncoding.DecodeString(str(chunk, "content"))
			if err != nil || str(chunk, "template_sha256") != str(manifest, "template_sha256") || str(chunk, "file") != name || num(chunk, "offset") != float64(offset) {
				t.Fatal("starter bytes changed during retrieval")
			}
			content = append(content, data...)
			total += len(data)
			if total > 2<<20 {
				t.Fatal("starter exceeded source bound")
			}
			next := int(num(chunk, "next_offset"))
			if next == -1 {
				break
			}
			if next != offset+len(data) || next <= offset {
				t.Fatal("template pagination did not advance")
			}
			offset = next
		}
		path := filepath.Join(j.path, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := prepareMCPToolSource(j.path, j.nonce(t, "fixture")); err != nil {
		t.Fatal(err)
	}
}

func (j *mcpToolJourney) planHosted(t *testing.T) map[string]any {
	upload := j.call(t, j.owner, "gregale_create_source_upload", map[string]any{"app": j.app}, false)
	u, err := url.Parse(str(upload, "upload_url"))
	bridge, bridgeErr := url.Parse(j.tg.mcpURL)
	if err != nil || bridgeErr != nil || u.Scheme != bridge.Scheme || u.Host != bridge.Host || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, bridge.Path+"/uploads/") {
		t.Fatal("upload URL escaped the selected hosted bridge")
	}
	body, err := archiveJourneyToolSource(j.path)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(j.ctx, http.MethodPut, u.String(), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal("hosted source upload failed")
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("hosted source upload: HTTP %d", response.StatusCode)
	}
	return j.call(t, j.owner, "gregale_plan_uploaded_tool_deploy", map[string]any{"app": j.app, "reason": "Opt-in staging hosted tool publication acceptance", "upload_id": str(upload, "upload_id"), "access_mode": "open", "allow_tool_execution": true}, false)
}

func archiveJourneyToolSource(root string) ([]byte, error) {
	var body bytes.Buffer
	gzipWriter := gzip.NewWriter(&body)
	writer := tar.NewWriter(gzipWriter)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("journey source must be regular files")
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err = writer.WriteHeader(&tar.Header{Name: filepath.ToSlash(name), Mode: 0o644, Size: int64(len(data))}); err != nil {
			return err
		}
		_, err = writer.Write(data)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err = writer.Close(); err != nil {
		return nil, err
	}
	if err = gzipWriter.Close(); err != nil {
		return nil, err
	}
	return body.Bytes(), nil
}
