package integration

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

const HostedClientSource = "src/gregale-hosted"

// HostedSpec contains reviewable metadata only. Assertions and tokens stay
// outside the generated application; only the assertion digest is persisted.
type HostedSpec struct {
	AppID         string `json:"app_id"`
	DeploymentID  string `json:"deployment_id"`
	APIHash       string `json:"api_hash"`
	Relation      string `json:"relation"`
	OrderBy       string `json:"order_by,omitempty"`
	Fingerprint   string `json:"fingerprint"`
	NumericMode   string `json:"numeric_mode"`
	InstallKey    string `json:"install_key"`
	AssertionHash string `json:"assertion_hash"`
	Bundle        bool   `json:"bundle"`
}

var hostedIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)
var hostedDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

func BuildHosted(directory, client string, spec HostedSpec, resume bool) (*Plan, error) {
	if spec.AppID == "" || spec.DeploymentID == "" || !hostedDigest.MatchString(spec.APIHash) || !hostedIdentifier.MatchString(spec.Relation) || (spec.OrderBy != "" && !hostedIdentifier.MatchString(spec.OrderBy)) || !hostedDigest.MatchString(spec.Fingerprint) || !hostedDigest.MatchString(spec.InstallKey) || !hostedDigest.MatchString(spec.AssertionHash) {
		return nil, errors.New("invalid hosted integration relation, order column, or digest")
	}
	return build(directory, client, spec.NumericMode, resume, &spec)
}

func buildHostedPlan(p *Plan, numericMode string, spec HostedSpec) (*Plan, error) {
	client := p.ClientDirectory
	p.GeneratedTypes = filepath.ToSlash(filepath.Join(client, HostedClientSource, "database.types.ts"))
	p.reserved = []string{filepath.Join(client, HostedClientSource)}
	if spec.Bundle {
		p.reserved = append(p.reserved, "data-api-artifacts.json", filepath.Join(client, "vendor", "gregale-data.tgz"))
	}
	for _, name := range append(append([]string{}, p.reserved...), filepath.Join(client, "tsconfig.json"), "package-lock.json", "node_modules", filepath.Join(client, "package-lock.json"), filepath.Join(client, "node_modules")) {
		if err := safePath(p.Directory, name); err != nil {
			return nil, err
		}
	}
	if p.WorkspaceRoot != "" {
		for _, name := range []string{"package.json", "package-lock.json", "node_modules"} {
			if err := safePath(p.Directory, filepath.Join(p.WorkspaceRoot, name)); err != nil {
				return nil, err
			}
		}
	}
	info, err := os.Lstat(filepath.Join(p.Directory, client, "tsconfig.json"))
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("the selected client needs an existing regular tsconfig.json")
	}
	for _, name := range p.reserved {
		if _, err := os.Lstat(filepath.Join(p.Directory, name)); !p.Resume && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("hosted integration destination already exists: %s", name)
		}
	}
	relation, _ := json.Marshal(spec.Relation)
	fingerprint, _ := json.Marshal(spec.Fingerprint)
	mode, _ := json.Marshal(numericMode)
	order := ""
	if spec.OrderBy != "" {
		encoded, _ := json.Marshal(spec.OrderBy)
		order = ".order(" + string(encoded) + ")"
	}
	clientSource := fmt.Sprintf(`import { createDataClient, type DataClientOptions } from '@gregale/data'
import type { Database } from './database.types.js'

/** Supply the hosted API origin and your application's access-token provider. */
export function createHostedData(options: DataClientOptions) {
  const client = createDataClient<Database>({ ...options, numericMode: %s, expectedSchemaFingerprint: %s }).schema('public')
  return { read: () => client.from(%s).select('*')%s.limit(100) }
}
`, mode, fingerprint, relation, order)
	setup, _ := json.MarshalIndent(spec, "", "  ")
	config := "{\n  \"extends\": \"../../tsconfig.json\",\n  \"compilerOptions\": { \"noEmit\": true },\n  \"include\": [\"client.ts\", \"database.types.ts\"]\n}\n"
	readme := "# Hosted Data API client\n\nPass the hosted API origin and the application's access-token provider to `createHostedData`:\n\n```ts\nimport { createHostedData } from './gregale-hosted/client.js'\n\nconst data = createHostedData({ url: dataApiUrl, accessToken: () => auth.getAccessToken() })\nconst { data: rows, error } = await data.read()\n```\n\nSupply `dataApiUrl` and `auth` from the application. The `read()` method queries the selected public relation with the verified schema fingerprint. Review expected rows and authorization separately.\n"
	for name, content := range map[string]string{"client.ts": clientSource, "tsconfig.json": config, "setup.json": string(setup) + "\n", "README.md": readme} {
		p.Files = append(p.Files, File{Path: filepath.ToSlash(filepath.Join(client, HostedClientSource, name)), Action: "create", Content: content, mode: 0644})
	}
	sort.Slice(p.Files, func(i, j int) bool { return p.Files[i].Path < p.Files[j].Path })
	if p.Resume {
		for _, f := range p.Files {
			if err := safePath(p.Directory, f.Path); err != nil {
				return nil, err
			}
			info, err := os.Lstat(filepath.Join(p.Directory, f.Path))
			if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
				return nil, fmt.Errorf("cannot resume: %s is missing or not regular", f.Path)
			}
			if filepath.Base(f.Path) == "README.md" {
				continue
			}
			data, err := os.ReadFile(filepath.Join(p.Directory, f.Path))
			if err != nil || string(data) != f.Content {
				return nil, fmt.Errorf("cannot resume: %s changed; preserve and review it", f.Path)
			}
		}
		p.Files = nil
	}
	return p, nil
}
