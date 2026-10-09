package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type profileSourceAttribution struct {
	Status, Reason, Path, DiffURL string
	CPUIncrease                   float64
	SharePercent                  *float64
}

func (s *server) attributeCanaryFinding(ctx context.Context, acct state.Account, app state.App, signal *api.CanaryProfileSignal, evidence api.ProfileRegressionEvidence) profileSourceAttribution {
	out := profileSourceAttribution{Status: "Attribution unavailable", Reason: "Both revisions and a matching repository source path are required.", CPUIncrease: evidence.Metric.DeltaCPUPerSecond}
	if signal.Total != nil && signal.Total.DeltaCPUPerSecond > 0 {
		share := 100 * evidence.Metric.DeltaCPUPerSecond / signal.Total.DeltaCPUPerSecond
		if !math.IsNaN(share) && !math.IsInf(share, 0) {
			out.SharePercent = &share
		}
	}
	s.enrichCanaryProfileSignal(ctx, app, signal, nil)
	b, c := signal.BaselineSource, signal.CandidateSource
	if b == nil || c == nil || !b.Available || !c.Available || !strings.EqualFold(b.Repository, c.Repository) {
		return out
	}
	// Attribute the selected function, or the leaf of an inclusive call path.
	if len(evidence.Frames) == 0 {
		return out
	}
	f := evidence.Frames[len(evidence.Frames)-1]
	if f.BaselineSource == nil || f.CandidateSource == nil || f.BaselineSource.Path != f.CandidateSource.Path {
		return out
	}
	out.Path = f.BaselineSource.Path
	hash := sha256.Sum256([]byte(out.Path))
	out.DiffURL = "https://github.com/" + c.Repository + "/compare/" + b.CommitSHA + ".." + c.CommitSHA + "#diff-" + fmt.Sprintf("%x", hash)
	if b.CommitSHA == c.CommitSHA {
		out.Status = "Source unchanged"
		out.Reason = "Both deployments use the same recorded source commit."
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	install, problem := s.resolveInstallToken(ctx, acct, app, c.Repository)
	if problem != nil || s.githubd == nil {
		out.Reason = "GitHub repository access is unavailable."
		return out
	}
	token, _, err := s.githubd.MintInstallationToken(ctx, acct.ID, install)
	if err != nil || token == "" {
		out.Reason = "GitHub repository access is unavailable."
		return out
	}
	client := &http.Client{Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	old, err := profileFileBlob(ctx, client, token, b.Repository, out.Path, b.CommitSHA)
	if err != nil {
		out.Reason = "The stable source file could not be verified."
		return out
	}
	next, err := profileFileBlob(ctx, client, token, c.Repository, out.Path, c.CommitSHA)
	if err != nil {
		out.Reason = "The canary source file could not be verified."
		return out
	}
	out.Status = "Source file changed"
	out.Reason = "The selected frame's source file has different Git blob IDs at the recorded commits. This does not establish that the function changed or caused the regression."
	if old == next {
		out.Status = "Source unchanged"
		out.Reason = "The selected frame's source file has the same Git blob ID at both recorded commits."
	}
	return out
}

func profileFileBlob(ctx context.Context, client *http.Client, token, repo, path, commit string) (string, error) {
	parts := strings.Split(path, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+repo+"/contents/"+strings.Join(parts, "/")+"?ref="+url.QueryEscape(commit), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github.object+json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("source unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(body) > 2<<20 {
		return "", fmt.Errorf("source metadata exceeds limit")
	}
	var file struct {
		SHA  string `json:"sha"`
		Type string `json:"type"`
	}
	if json.Unmarshal(body, &file) != nil || file.Type != "file" || len(file.SHA) != 40 {
		return "", fmt.Errorf("source metadata unavailable")
	}
	if _, err := hex.DecodeString(file.SHA); err != nil {
		return "", fmt.Errorf("invalid source blob ID")
	}
	return file.SHA, nil
}
