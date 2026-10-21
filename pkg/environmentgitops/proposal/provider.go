package proposal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Call sends a single authenticated JSON API request. It must not retry writes
// or include provider bodies (which can contain manifest values) in errors.
type Call func(context.Context, string, string, any, any) error

type Client struct {
	Provider string
	Path     string
	Call     Call
}

func (c Client) endpoint() string {
	if c.Provider == "gitlab" {
		return "projects/" + url.PathEscape(c.Path)
	}
	return "repos/" + c.Path
}

func (c Client) Repository(ctx context.Context) (Repository, error) {
	var out struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
		Path     string `json:"path_with_namespace"`
		Archived bool   `json:"archived"`
	}
	if err := c.Call(ctx, "GET", c.endpoint(), nil, &out); err != nil {
		return Repository{}, err
	}
	if out.Archived {
		return Repository{}, fmt.Errorf("target repository is archived")
	}
	if c.Provider == "gitlab" {
		out.FullName = out.Path
	}
	return Repository{ID: out.ID, Path: out.FullName}, nil
}

func (c Client) Head(ctx context.Context, branch string) (string, error) {
	if c.Provider == "gitlab" {
		var out struct {
			Name   string `json:"name"`
			Commit struct {
				ID string `json:"id"`
			} `json:"commit"`
		}
		err := c.Call(ctx, "GET", c.endpoint()+"/repository/branches/"+url.PathEscape(branch), nil, &out)
		if err != nil {
			return "", err
		}
		if out.Name != branch || !validHex(out.Commit.ID, 40) {
			return "", fmt.Errorf("invalid provider branch identity")
		}
		return out.Commit.ID, nil
	}
	var out struct {
		Ref    string `json:"ref"`
		Object struct {
			SHA  string `json:"sha"`
			Type string `json:"type"`
		} `json:"object"`
	}
	err := c.Call(ctx, "GET", c.endpoint()+"/git/ref/heads/"+escapePath(branch), nil, &out)
	if err != nil {
		return "", err
	}
	if out.Ref != "refs/heads/"+branch || out.Object.Type != "commit" || !validHex(out.Object.SHA, 40) {
		return "", fmt.Errorf("invalid provider branch identity")
	}
	return out.Object.SHA, nil
}

func escapePath(path string) string {
	parts := strings.Split(path, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

func (c Client) Reviews(ctx context.Context, branch, base string) ([]Review, error) {
	if c.Provider == "gitlab" {
		var rows []json.RawMessage
		err := c.Call(ctx, "GET", c.endpoint()+"/merge_requests?state=all&scope=all&source_branch="+url.QueryEscape(branch)+"&per_page=2", nil, &rows)
		if err != nil {
			return nil, err
		}
		out := make([]Review, 0, len(rows))
		for _, row := range rows {
			var listed gitlabReview
			if json.Unmarshal(row, &listed) != nil {
				return nil, fmt.Errorf("invalid provider review response")
			}
			// List responses can omit the current head. Read the selected request.
			if listed.Number <= 0 {
				return nil, fmt.Errorf("invalid provider review identity")
			}
			var item gitlabReview
			if err := c.Call(ctx, "GET", c.endpoint()+"/merge_requests/"+strconv.FormatInt(listed.Number, 10), nil, &item); err != nil {
				return nil, err
			}
			if item.Number != listed.Number {
				return nil, fmt.Errorf("provider review identity changed")
			}
			out = append(out, item.review())
		}
		return out, nil
	}
	var rows []githubReview
	owner := strings.Split(c.Path, "/")[0]
	err := c.Call(ctx, "GET", c.endpoint()+"/pulls?state=all&head="+url.QueryEscape(owner+":"+branch)+"&per_page=2", nil, &rows)
	if err != nil {
		return nil, err
	}
	out := make([]Review, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.review())
	}
	return out, nil
}

type githubReview struct {
	Number int64  `json:"number"`
	URL    string `json:"html_url"`
	State  string `json:"state"`
	Body   string `json:"body"`
	Head   struct {
		Ref, SHA string
		Repo     struct{ ID int64 }
	} `json:"head"`
	Base struct {
		Ref  string
		Repo struct{ ID int64 }
	} `json:"base"`
}

func (r githubReview) review() Review {
	return Review{Number: r.Number, URL: r.URL, State: r.State, Branch: r.Head.Ref, Base: r.Base.Ref, Head: r.Head.SHA, Body: r.Body, RepositoryID: r.Head.Repo.ID, TargetRepositoryID: r.Base.Repo.ID}
}

type gitlabReview struct {
	Number   int64  `json:"iid"`
	URL      string `json:"web_url"`
	State    string `json:"state"`
	Body     string `json:"description"`
	Head     string `json:"sha"`
	Branch   string `json:"source_branch"`
	Base     string `json:"target_branch"`
	SourceID int64  `json:"source_project_id"`
	TargetID int64  `json:"target_project_id"`
}

func (r gitlabReview) review() Review {
	state := r.State
	if state == "opened" {
		state = "open"
	}
	return Review{Number: r.Number, URL: r.URL, State: state, Body: r.Body, Head: r.Head, Branch: r.Branch, Base: r.Base, RepositoryID: r.SourceID, TargetRepositoryID: r.TargetID}
}

func (c Client) Open(ctx context.Context, branch, base, title, body string) (Review, error) {
	if c.Provider == "gitlab" {
		var out gitlabReview
		err := c.Call(ctx, "POST", c.endpoint()+"/merge_requests", map[string]any{"source_branch": branch, "target_branch": base, "title": "Draft: " + title, "description": body, "remove_source_branch": false}, &out)
		return out.review(), err
	}
	var out githubReview
	err := c.Call(ctx, "POST", c.endpoint()+"/pulls", map[string]any{"head": branch, "base": base, "title": title, "body": body, "draft": true, "maintainer_can_modify": false}, &out)
	return out.review(), err
}
