// Package sourcecheck verifies retained authored source against its pure review DTO.
package sourcecheck

import (
	"bytes"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/markers"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/tasktoolsource"
	"reflect"
	"strings"
)

func ValidatePrepare(account string, in api.HostedTaskSourcePrepare) (tasktoolsource.Review, error) {
	r, err := tasktoolsource.Decode(in.Review)
	if err != nil || r.AccountID != account || len(in.Sources) != 2 {
		return r, tasktoolsource.ErrInvalid
	}
	var web, worker mcphosting.Config
	for _, role := range []string{"web", "worker"} {
		entries, ok := in.Sources[role]
		if !ok {
			return r, tasktoolsource.ErrInvalid
		}
		digest, err := tasktoolsource.Digest(entries)
		if err != nil || digest != r.Sources[role] {
			return r, tasktoolsource.ErrInvalid
		}
		cfg, err := SourceConfig(entries, role == "worker")
		if err != nil {
			return r, tasktoolsource.ErrInvalid
		}
		if role == "web" {
			web = cfg
		} else {
			worker = cfg
		}
	}
	if r.Endpoint != web.Endpoint || r.AccessMode != web.Auth.Mode || !reflect.DeepEqual(r.ToolScopes, map[string][]string(web.Auth.ToolScopes)) || r.WebLimits != mcphosting.ReviewTaskLimits(*web.Tasks) || r.WorkerLimits != mcphosting.ReviewTaskLimits(*worker.Tasks) {
		return r, tasktoolsource.ErrInvalid
	}
	roles := map[string][]string{}
	for _, a := range r.Apps {
		roles[a.Role] = append(roles[a.Role], a.App)
	}
	if !reflect.DeepEqual(roles["web"], []string{r.WebApp}) || !reflect.DeepEqual(roles["worker"], []string{r.WorkerApp}) || !reflect.DeepEqual(roles["observer"], []string{r.ObserverApp}) || !bytes.Equal(mustJSON(roles["previous_worker"]), mustJSON(append([]string(nil), r.PreviousWorkerApps...))) {
		return r, tasktoolsource.ErrInvalid
	}
	return r, nil
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func SourceConfig(entries []api.HostedTaskSourceEntry, worker bool) (mcphosting.Config, error) {
	files := map[string][]byte{}
	for _, e := range entries {
		if !e.Directory {
			files[e.Path] = e.Content
			if !strings.Contains(e.Path, "/") {
				family := markers.MarkerFor(e.Path)
				if family != markers.FrameworkUnknown && family != markers.FrameworkNode {
					return mcphosting.Config{}, tasktoolsource.ErrInvalid
				}
			}
		}
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if len(files["package.json"]) > api.MCPInlineSourceMaxBytes || json.Unmarshal(files["package.json"], &pkg) != nil || pkg.Scripts["start"] == "" {
		return mcphosting.Config{}, tasktoolsource.ErrInvalid
	}
	cfg, err := mcphosting.Decode(strings.NewReader(string(files[mcphosting.ConfigFile])))
	if err != nil || cfg.Tasks == nil || cfg.Tasks.Enabled == nil || !*cfg.Tasks.Enabled {
		return cfg, tasktoolsource.ErrInvalid
	}
	if worker {
		if _, ok := files["tasks-release.js"]; !ok {
			return cfg, tasktoolsource.ErrInvalid
		}
	}
	return cfg, nil
}
