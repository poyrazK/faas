package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/onebox-faas/faas/pkg/api"
)

// `gregale project export <project> --format compose` writes a project's
// services back as a plain Compose file, so a team can run it elsewhere: the
// microservices-demo feedback (2026-11-16) named the Gregale-only
// configuration as the main adoption risk. Gregale settings travel as the
// x-gregale-* keys the Compose importer reads, so the file deploys back the
// same way. Env values are sealed and never leave Gregale: each variable is
// referenced by name, for `gregale env pull` or the operator's own .env.

const projectExportUsage = "usage: gregale project export <slug> [--format compose] [--out PATH]"

type composeExport struct {
	Name     string                    `yaml:"name"`
	Services map[string]composeService `yaml:"services"`
}

type composeService struct {
	Build       string            `yaml:"build"`
	Ports       []string          `yaml:"ports,omitempty"`
	Expose      []string          `yaml:"expose,omitempty"`
	Environment map[string]string `yaml:"environment,omitempty"`
	DependsOn   []string          `yaml:"depends_on,omitempty"`
	Deploy      *composeDeploy    `yaml:"deploy,omitempty"`
	AppProtocol string            `yaml:"x-gregale-app-protocol,omitempty"`
	Streaming   bool              `yaml:"x-gregale-streaming,omitempty"`
}

type composeDeploy struct {
	Resources composeResources `yaml:"resources"`
}

type composeResources struct {
	Limits composeLimits `yaml:"limits"`
}

type composeLimits struct {
	Memory string `yaml:"memory"`
}

// projectExportClient is the read surface the export needs.
type projectExportClient interface {
	GetProject(ctx context.Context, slug string) (api.ProjectResponse, error)
	GetApp(ctx context.Context, slug string) (api.AppResponse, error)
	GetAppsSlugEnv(ctx context.Context, slug string) (api.AppEnvListResponse, error)
	ListAppDeployments(ctx context.Context, slug, before string, limit int) (api.DeploymentListResponse, error)
}

func cmdProjectsExport(args []string) int {
	fs := newFlagSet("project export", flag.ContinueOnError)
	format := fs.String("format", "compose", "output format: compose")
	out := fs.String("out", "", "write to PATH instead of stdout")
	flagArgs, positional := normalizeDebugFlagArgs(args, map[string]bool{"format": true, "out": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 1
	}
	if len(positional) != 1 || !api.ValidProjectSlug(positional[0]) {
		PrintUsage(os.Stderr, projectExportUsage, "projects")
		return 1
	}
	if *format != "compose" {
		return printErr("Unsupported format", fmt.Errorf("--format %q is not supported; use compose", *format))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	doc, notes, err := exportProjectCompose(context.Background(), client, positional[0])
	if err != nil {
		return printErr("Could not export project", err)
	}
	if *out == "" {
		_, _ = osStdout.Write(doc)
	} else {
		if err := os.WriteFile(*out, doc, 0o644); err != nil {
			return printErr("Could not write file", err)
		}
		PrintProgress(osStderr, "wrote %s", *out)
	}
	for _, note := range notes {
		PrintWarn(osStderr, "%s", note)
	}
	return 0
}

// exportProjectCompose renders the project's services as a Compose file and
// returns the notes the operator should read alongside it.
func exportProjectCompose(ctx context.Context, client projectExportClient, slug string) ([]byte, []string, error) {
	project, err := client.GetProject(ctx, slug)
	if err != nil {
		return nil, nil, err
	}
	if len(project.Workloads) == 0 {
		return nil, nil, errors.New("project has no services to export")
	}
	export := composeExport{Name: slug, Services: map[string]composeService{}}
	var notes []string
	secrets := 0
	for _, workload := range project.Workloads {
		name := workload.WorkloadName
		if name == "" {
			name = workload.Slug
		}
		app, err := client.GetApp(ctx, workload.Slug)
		if err != nil {
			return nil, nil, fmt.Errorf("read service %s: %w", name, err)
		}
		service := composeService{Build: exportBuildContext(ctx, client, workload.Slug, name), DependsOn: slices.Clone(workload.DependsOn)}
		if service.Build == "./"+name {
			notes = append(notes, fmt.Sprintf("%s: no build root recorded; build: ./%s is a guess, check it", name, name))
		}
		if port := exportPort(app.Manifest); port > 0 {
			p := strconv.Itoa(port)
			if app.Visibility == "internal" {
				service.Expose = []string{p}
			} else {
				service.Ports = []string{p + ":" + p}
			}
		}
		if env, err := client.GetAppsSlugEnv(ctx, workload.Slug); err == nil && len(env.Env) > 0 {
			service.Environment = map[string]string{}
			for _, row := range env.Env {
				service.Environment[row.Key] = "${" + row.Key + "}"
			}
			secrets += len(env.Env)
		}
		if app.RAMMB > 0 {
			service.Deploy = &composeDeploy{Resources: composeResources{Limits: composeLimits{Memory: strconv.Itoa(app.RAMMB) + "M"}}}
		}
		if app.AppProtocol != "" && app.AppProtocol != "http1" {
			service.AppProtocol = app.AppProtocol
		}
		service.Streaming = app.StreamingEnabled
		export.Services[name] = service
	}
	body, err := yaml.Marshal(export)
	if err != nil {
		return nil, nil, err
	}
	var doc bytes.Buffer
	fmt.Fprintf(&doc, "# Exported from Gregale project %q by `gregale project export`.\n", slug)
	doc.WriteString("# Env values are not exported: each variable is referenced by name.\n")
	doc.WriteString("# Fill them from a .env file (`gregale env pull` writes one per app).\n")
	doc.WriteString("# x-gregale-* keys are Gregale settings; other Compose tools ignore them.\n")
	doc.Write(body)
	if secrets > 0 {
		notes = append(notes, fmt.Sprintf("%d env variables are referenced by name only; set their values before running the file elsewhere", secrets))
	}
	return doc.Bytes(), notes, nil
}

// exportBuildContext is the service's build root from its newest live
// deployment (the repository-relative root the project deploy recorded),
// falling back to ./<service>.
func exportBuildContext(ctx context.Context, client projectExportClient, slug, name string) string {
	deployments, err := client.ListAppDeployments(ctx, slug, "", 20)
	if err == nil {
		for _, d := range deployments.Items {
			if d.Status != "live" {
				continue
			}
			if root := strings.Trim(path.Clean("/"+d.SourceRoot), "/"); root != "" {
				return "./" + root
			}
			break
		}
	}
	return "./" + name
}

// exportPort is the service's listening port: the manifest port, else its
// first declared port.
func exportPort(m api.AppManifest) int {
	if m.Port > 0 {
		return m.Port
	}
	for _, p := range m.Ports {
		if p.Port > 0 {
			return p.Port
		}
	}
	return 0
}
