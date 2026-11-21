// Package runenv owns immutable dependency recipes for disposable source Runs.
package runenv

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runfile"
)

var ErrRecipe = errors.New("runenv: invalid dependency recipe")
var pythonPin = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*==[A-Za-z0-9][A-Za-z0-9.!+_-]*$`)
var npmName = regexp.MustCompile(`^(?:@[a-z0-9._-]+/)?[a-z0-9][a-z0-9._-]*$`)
var npmVersion = regexp.MustCompile(`^[A-Za-z0-9 .^~<>=*|+-]+$`)
var hashPin = regexp.MustCompile(`^--hash=sha256:[0-9a-f]{64}$`)
var imageRef = regexp.MustCompile(`^[a-z0-9][a-z0-9./:_-]*@sha256:[0-9a-f]{64}$`)
var systemPin = regexp.MustCompile(`^[a-z0-9][a-z0-9+_.-]*=[0-9][A-Za-z0-9+_.~]*-r[0-9]+$`)

// PublicStatus hides the versioned worker lane from customer receipts.
func PublicStatus(status string) string { return strings.TrimSuffix(status, "_system_v1") }

func Canonical(r api.CreateExecutionEnvironmentRequest) api.CreateExecutionEnvironmentRequest {
	r.SystemPackages = append([]string(nil), r.SystemPackages...)
	sort.Strings(r.SystemPackages)
	return r
}

func validateSystemPackages(packages []string) error {
	if len(packages) > api.ExecutionEnvironmentMaxSystemPackages {
		return ErrRecipe
	}
	seen := map[string]bool{}
	for _, pin := range packages {
		name, _, _ := strings.Cut(pin, "=")
		if len(pin) > api.ExecutionEnvironmentSystemPackageMaxBytes || !systemPin.MatchString(pin) || seen[name] {
			return ErrRecipe
		}
		seen[name] = true
	}
	return nil
}

func ValidBaseRef(ref string) bool { return imageRef.MatchString(ref) }
func ValidID(id string) bool {
	v, err := uuid.Parse(id)
	return err == nil && v != uuid.Nil && v.String() == id
}

// Validate admits only public, integrity-checked registries. Installation runs
// in the builder VM; untrusted package metadata never becomes a host command.
func Validate(r api.CreateExecutionEnvironmentRequest) error {
	if !r.Runtime.Valid() || len(r.Requirements)+len(r.PackageJSON)+len(r.PackageLock)+len(strings.Join(r.SystemPackages, "\n")) > api.ExecutionEnvironmentRecipeMaxBytes || validateSystemPackages(r.SystemPackages) != nil {
		return ErrRecipe
	}
	if r.Requirements == "" && r.PackageJSON == "" && r.PackageLock == "" && len(r.SystemPackages) > 0 {
		return nil
	}
	switch r.Runtime {
	case api.ExecutionRuntimePython312, api.ExecutionRuntimePython313:
		if r.Requirements == "" || r.PackageJSON != "" || r.PackageLock != "" {
			return ErrRecipe
		}
		lines := strings.Split(strings.ReplaceAll(r.Requirements, "\\\r\n", " "), "\n")
		joined := strings.ReplaceAll(strings.Join(lines, "\n"), "\\\n", " ")
		count := 0
		for _, line := range strings.Split(joined, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			words := strings.Fields(line)
			if len(words) < 2 || !pythonPin.MatchString(words[0]) {
				return ErrRecipe
			}
			for _, word := range words[1:] {
				if !hashPin.MatchString(word) {
					return ErrRecipe
				}
			}
			count++
		}
		if count == 0 || count > api.ExecutionEnvironmentMaxPackages {
			return ErrRecipe
		}
	case api.ExecutionRuntimeNode22, api.ExecutionRuntimeNode24:
		if r.Requirements != "" || r.PackageJSON == "" || r.PackageLock == "" {
			return ErrRecipe
		}
		var manifest struct {
			Dependencies    map[string]string `json:"dependencies"`
			DevDependencies map[string]string `json:"devDependencies"`
			Workspaces      json.RawMessage   `json:"workspaces"`
		}
		if json.Unmarshal([]byte(r.PackageJSON), &manifest) != nil || len(manifest.Workspaces) > 0 || len(manifest.Dependencies)+len(manifest.DevDependencies) == 0 {
			return ErrRecipe
		}
		var raw map[string]json.RawMessage
		if json.Unmarshal([]byte(r.PackageJSON), &raw) != nil {
			return ErrRecipe
		}
		for key := range raw {
			switch key {
			case "name", "version", "private", "type", "description", "license", "scripts", "engines", "dependencies", "devDependencies":
			default:
				return ErrRecipe
			}
		}
		for _, deps := range []map[string]string{manifest.Dependencies, manifest.DevDependencies} {
			for name, version := range deps {
				if !npmName.MatchString(name) || !npmVersion.MatchString(version) {
					return ErrRecipe
				}
			}
		}
		var lock struct {
			Version  int `json:"lockfileVersion"`
			Packages map[string]struct {
				Resolved  string `json:"resolved"`
				Integrity string `json:"integrity"`
				Link      bool   `json:"link"`
			} `json:"packages"`
		}
		if json.Unmarshal([]byte(r.PackageLock), &lock) != nil || lock.Version != 3 || len(lock.Packages) < 2 || len(lock.Packages) > api.ExecutionEnvironmentMaxPackages+1 {
			return ErrRecipe
		}
		for name, p := range lock.Packages {
			if name == "" {
				continue
			}
			u, err := url.Parse(p.Resolved)
			if p.Link || err != nil || u.Scheme != "https" || u.Host != "registry.npmjs.org" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
				return ErrRecipe
			}
			if !strings.HasPrefix(name, "node_modules/") || strings.Contains(name, "..") || strings.ContainsAny(name, "\\\x00") {
				return ErrRecipe
			}
			parts := strings.Split(p.Integrity, "-")
			if len(parts) != 2 || parts[0] != "sha512" {
				return ErrRecipe
			}
			hash, err := base64.StdEncoding.DecodeString(parts[1])
			if err != nil || len(hash) != 64 {
				return ErrRecipe
			}
		}
	default:
		return ErrRecipe
	}
	return nil
}

// Digest includes the immutable operator-selected installer image and the
// recipe format so cache reuse cannot cross runtime or toolchain changes.
func Digest(r api.CreateExecutionEnvironmentRequest, base string) (string, error) {
	if Validate(r) != nil || !ValidBaseRef(base) {
		return "", ErrRecipe
	}
	r = Canonical(r)
	format := "run-environment/v1"
	if len(r.SystemPackages) > 0 {
		format = "run-environment/system-v1"
	}
	body, _ := json.Marshal(struct {
		Format  string
		Base    string
		Request api.CreateExecutionEnvironmentRequest
	}{format, base, r})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

// SourceTar generates a fixed Dockerfile, not caller-authored build commands.
func SourceTar(id string, r api.CreateExecutionEnvironmentRequest, base string) ([]byte, error) {
	if !ValidID(id) || Validate(r) != nil || !ValidBaseRef(base) {
		return nil, ErrRecipe
	}
	files := map[string]string{}
	r = Canonical(r)
	root := "/opt/gregale/environment"
	dockerfile := "FROM " + base + "\nUSER root\nWORKDIR " + root + "\n"
	if len(r.SystemPackages) > 0 {
		command, _ := json.Marshal(append([]string{"apk", "add", "--no-cache", "--"}, r.SystemPackages...))
		dockerfile += "RUN " + string(command) + "\n"
		var check []string
		switch r.Runtime {
		case api.ExecutionRuntimePython312, api.ExecutionRuntimePython313:
			minor := "12"
			if r.Runtime == api.ExecutionRuntimePython313 {
				minor = "13"
			}
			check = []string{"python3", "-I", "-c", "import sys; assert sys.version_info[:2] == (3," + minor + "), 'system packages changed the selected runtime'"}
		default:
			major := "22"
			if r.Runtime == api.ExecutionRuntimeNode24 {
				major = "24"
			}
			check = []string{"node", "--eval", "if (process.versions.node.split('.')[0] !== '" + major + "') process.exit(1)"}
		}
		command, _ = json.Marshal(check)
		dockerfile += "RUN " + string(command) + "\n"
	}
	switch r.Runtime {
	case api.ExecutionRuntimePython312, api.ExecutionRuntimePython313:
		if r.Requirements == "" {
			break
		}
		files["requirements.txt"] = r.Requirements
		dockerfile += "COPY requirements.txt ./requirements.txt\nRUN python3 -m pip --isolated install --disable-pip-version-check --no-cache-dir --ignore-installed --require-hashes --only-binary=:all: --index-url https://pypi.org/simple --target " + root + "/python -r requirements.txt\n"
	default:
		if r.PackageJSON == "" {
			break
		}
		files["package.json"] = r.PackageJSON
		files["package-lock.json"] = r.PackageLock
		dockerfile += "COPY package.json package-lock.json ./\nRUN npm ci --ignore-scripts --no-audit --no-fund --registry=https://registry.npmjs.org && npm cache clean --force\n"
	}
	marker, _ := json.Marshal(map[string]any{"kind": "execution", "version": 1, "environment_id": id})
	files["execution.json"] = string(marker)
	dockerfile += "COPY execution.json /etc/faas/execution.json\nWORKDIR /app\n"
	files["Dockerfile"] = dockerfile
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		body := files[name]
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(body)), ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg}); err != nil {
			return nil, err
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func ArtifactKey(account, id, lease, kind string) string {
	return fmt.Sprintf("run-environments/%s/%s/%s/%s", account, id, lease, kind)
}

// ValidBaseKey is the closed namespace accepted by immutable base provenance.
func ValidBaseKey(key string) bool {
	p := strings.Split(key, "/")
	return len(p) == 5 && p[0] == "run-environments" && runfile.ValidOwnerID(p[1]) && ValidID(p[2]) && ValidID(p[3]) && p[4] == "base.ext4"
}
