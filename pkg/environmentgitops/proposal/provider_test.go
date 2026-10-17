package proposal

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// These fixtures exercise the real provider adapters, including raw tree modes,
// single-parent ancestry and complete one-file diffs. They make no network calls.
func providerReadFixture(t *testing.T, provider string, mutate string) (Client, File) {
	t.Helper()
	base, head, tree, subtree := strings.Repeat("a", 40), strings.Repeat("c", 40), strings.Repeat("d", 40), strings.Repeat("e", 40)
	raw := []byte("reviewed manifest bytes")
	blob := gitBlob(raw)
	file := File{Content: raw, Mode: "100644", Blob: blob, Tree: tree, LastCommit: base}
	client := Client{Provider: provider, Path: "shop/platform"}
	client.Call = func(_ context.Context, method, endpoint string, input, output any) error {
		if method != "GET" {
			t.Fatal("read proof wrote", method)
		}
		var response any
		switch {
		case strings.Contains(endpoint, "/git/commits/"):
			response = map[string]any{"sha": head, "tree": map[string]string{"sha": tree}}
		case strings.Contains(endpoint, "/git/trees/"):
			mode := "100644"
			if mutate == "symlink" {
				mode = "120000"
			}
			if mutate == "executable" {
				mode = "100755"
			}
			entries := []map[string]string{{"path": "production.yaml", "mode": mode, "type": "blob", "sha": blob}}
			sha := subtree
			if strings.HasSuffix(endpoint, tree) {
				sha = tree
				entries = []map[string]string{{"path": "environments", "mode": "040000", "type": "tree", "sha": subtree}}
			}
			response = map[string]any{"sha": sha, "tree": entries, "truncated": mutate == "truncated-tree"}
		case strings.Contains(endpoint, "/git/blobs/"):
			content := raw
			if mutate == "content" {
				content = []byte("tampered")
			}
			response = map[string]string{"sha": blob, "encoding": "base64", "content": base64.StdEncoding.EncodeToString(content)}
		case strings.Contains(endpoint, "/repository/files/"):
			content := raw
			if mutate == "content" {
				content = []byte("tampered")
			}
			response = map[string]string{"file_path": "environments/production.yaml", "blob_id": blob, "encoding": "base64", "content": base64.StdEncoding.EncodeToString(content), "commit_id": head, "last_commit_id": base}
		case strings.Contains(endpoint, "/repository/tree?"):
			mode := "100644"
			if mutate == "symlink" {
				mode = "120000"
			}
			if mutate == "executable" {
				mode = "100755"
			}
			response = []map[string]string{{"path": "environments/production.yaml", "type": "blob", "mode": mode, "id": blob}}
		case strings.Contains(endpoint, "/repository/commits/") && strings.Contains(endpoint, "/diff?"):
			diff := map[string]any{"old_path": "environments/production.yaml", "new_path": "environments/production.yaml", "a_mode": "100644", "b_mode": "100644", "diff": "reviewed change"}
			if mutate == "rename" {
				diff["renamed_file"] = true
			}
			if mutate == "mode" {
				diff["b_mode"] = "100755"
			}
			if mutate == "truncated-diff" {
				diff["too_large"] = true
			}
			diffs := []map[string]any{diff}
			if mutate == "extra-file" {
				diffs = append(diffs, map[string]any{"new_path": "other"})
			}
			response = diffs
		case strings.Contains(endpoint, "/repository/commits/"):
			parents := []string{base}
			if mutate == "ancestry" {
				parents = append(parents, head)
			}
			response = map[string]any{"id": head, "parent_ids": parents}
		case strings.Contains(endpoint, "/commits/"):
			parents := []map[string]string{{"sha": base}}
			if mutate == "ancestry" {
				parents[0]["sha"] = head
			}
			diff := map[string]string{"filename": "environments/production.yaml", "status": "modified", "sha": blob}
			if mutate == "rename" {
				diff["previous_filename"] = "other"
			}
			if mutate == "mode" {
				diff["status"] = "renamed"
			}
			files := []map[string]string{diff}
			if mutate == "extra-file" {
				files = append(files, map[string]string{"filename": "other"})
			}
			response = map[string]any{"sha": head, "parents": parents, "files": files}
		default:
			return fmt.Errorf("unexpected endpoint: %s", endpoint)
		}
		encoded, err := json.Marshal(response)
		if err != nil {
			return err
		}
		return json.Unmarshal(encoded, output)
	}
	return client, file
}

func TestGitLabRejectsTruncatedDiff(t *testing.T) {
	c, file := providerReadFixture(t, "gitlab", "truncated-diff")
	if err := c.Verify(t.Context(), strings.Repeat("c", 40), strings.Repeat("a", 40), "environments/production.yaml", file); err == nil {
		t.Fatal("truncated diff accepted")
	}
}

func TestProviderVerifiesExactOneFileCommitAndRejectsMutations(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		for _, mode := range []string{"valid", "extra-file", "ancestry", "rename", "mode", "symlink", "executable", "content"} {
			t.Run(provider+"/"+mode, func(t *testing.T) {
				c, file := providerReadFixture(t, provider, mode)
				err := c.Verify(t.Context(), strings.Repeat("c", 40), strings.Repeat("a", 40), "environments/production.yaml", file)
				if (err == nil) != (mode == "valid") {
					t.Fatal("proof accepted wrong result", err)
				}
			})
		}
	}
	c, _ := providerReadFixture(t, "github", "truncated-tree")
	if _, err := c.File(t.Context(), strings.Repeat("c", 40), "environments/production.yaml"); err == nil {
		t.Fatal("truncated tree accepted")
	}
}

func TestProviderCreatesExclusiveCommitAndDraftReview(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		t.Run(provider, func(t *testing.T) {
			base, head, tree := strings.Repeat("a", 40), strings.Repeat("c", 40), strings.Repeat("d", 40)
			c := Client{Provider: provider, Path: "shop/platform"}
			calls := 0
			c.Call = func(_ context.Context, method, endpoint string, input, output any) error {
				calls++
				if method != "POST" {
					t.Fatal("unexpected mutation method", method)
				}
				body := input.(map[string]any)
				var response any
				switch {
				case strings.HasSuffix(endpoint, "/repository/commits"):
					if body["branch"] != "proposal" || body["start_sha"] != base || body["force"] != false {
						t.Fatal("missing exclusive branch creation", body)
					}
					actions := body["actions"].([]map[string]any)
					if len(actions) != 1 || actions[0]["last_commit_id"] != base || actions[0]["file_path"] != "environment.yaml" || actions[0]["action"] != "update" {
						t.Fatal("unsafe commit actions", actions)
					}
					response = map[string]string{"id": head}
				case strings.HasSuffix(endpoint, "/git/trees"):
					if body["base_tree"] != tree {
						t.Fatal("wrong base tree")
					}
					entries := body["tree"].([]map[string]any)
					if len(entries) != 1 || entries[0]["path"] != "environment.yaml" || entries[0]["mode"] != "100644" || entries[0]["content"] != "reviewed" {
						t.Fatal("unsafe tree", entries)
					}
					response = map[string]string{"sha": tree}
				case strings.HasSuffix(endpoint, "/git/commits"):
					if body["parents"].([]string)[0] != base {
						t.Fatal("wrong parent")
					}
					response = map[string]string{"sha": head}
				case strings.HasSuffix(endpoint, "/git/refs"):
					if body["ref"] != "refs/heads/proposal" || body["sha"] != head || body["force"] != nil {
						t.Fatal("unsafe ref", body)
					}
					response = map[string]string{"ref": "refs/heads/proposal"}
				case strings.HasSuffix(endpoint, "/merge_requests"):
					if body["title"] != "Draft: Promote" || body["source_branch"] != "proposal" || body["target_branch"] != "main" {
						t.Fatal("unsafe MR", body)
					}
					response = map[string]any{"iid": 7, "web_url": "https://gitlab.com/shop/platform/-/merge_requests/7", "state": "opened", "source_branch": "proposal", "target_branch": "main", "sha": head, "description": "redacted body", "source_project_id": 42, "target_project_id": 42}
				case strings.HasSuffix(endpoint, "/pulls"):
					if body["draft"] != true || body["maintainer_can_modify"] != false || body["head"] != "proposal" || body["base"] != "main" {
						t.Fatal("unsafe PR", body)
					}
					response = map[string]any{"number": 7, "html_url": "https://github.com/shop/platform/pull/7", "state": "open", "body": "redacted body", "head": map[string]any{"ref": "proposal", "sha": head, "repo": map[string]int{"id": 42}}, "base": map[string]any{"ref": "main", "repo": map[string]int{"id": 42}}}
				default:
					t.Fatal("unexpected provider write", endpoint)
				}
				raw, _ := json.Marshal(response)
				return json.Unmarshal(raw, output)
			}
			created, err := c.Commit(t.Context(), base, "proposal", "environment.yaml", File{Mode: "100644", Tree: tree, LastCommit: base, Content: []byte("reviewed")}, "Promote")
			if err != nil || created != head {
				t.Fatal(created, err)
			}
			review, err := c.Open(t.Context(), "proposal", "main", "Promote", "redacted body")
			if err != nil || review.Head != head || review.RepositoryID != 42 || review.State != "open" {
				t.Fatal(review, err)
			}
			want := 4
			if provider == "gitlab" {
				want = 2
			}
			if calls != want {
				t.Fatal(calls)
			}
		})
	}
}

func TestProviderUsesExplicitEncodedRepositoryAndBranchIdentities(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		t.Run(provider, func(t *testing.T) {
			c := Client{Provider: provider, Path: "shop/platform"}
			calls := 0
			c.Call = func(_ context.Context, method, endpoint string, input, output any) error {
				calls++
				if method != "GET" || input != nil {
					t.Fatal("unexpected write")
				}
				var response any
				if calls == 1 {
					response = map[string]any{"id": 42, "full_name": c.Path, "path_with_namespace": c.Path}
				} else if provider == "gitlab" {
					if !strings.HasSuffix(endpoint, "branches/"+url.PathEscape("release/prod")) {
						t.Fatal(endpoint)
					}
					response = map[string]any{"name": "release/prod", "commit": map[string]string{"id": strings.Repeat("a", 40)}}
				} else {
					if !strings.HasSuffix(endpoint, "ref/heads/release/prod") {
						t.Fatal(endpoint)
					}
					response = map[string]any{"ref": "refs/heads/release/prod", "object": map[string]string{"type": "commit", "sha": strings.Repeat("a", 40)}}
				}
				raw, _ := json.Marshal(response)
				return json.Unmarshal(raw, output)
			}
			if repo, err := c.Repository(t.Context()); err != nil || repo.ID != 42 || repo.Path != c.Path {
				t.Fatal(repo, err)
			}
			if _, err := c.Head(t.Context(), "release/prod"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
