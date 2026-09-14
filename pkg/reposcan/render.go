package reposcan

import (
	"fmt"
	"io/fs"
	"sort"

	"gopkg.in/yaml.v3"
)

// renderDoc decodes the current Render Blueprint shape: services includes
// web, worker, private, cron, and key-value entries; databases is top-level.
// CronJobs remains as a compatibility input for older blueprints.
type renderDoc struct {
	Services  []renderService  `yaml:"services"`
	CronJobs  []renderCron     `yaml:"cronJobs"`
	Databases []renderDatabase `yaml:"databases"`
}
type renderService struct {
	Name         string         `yaml:"name"`
	Type         string         `yaml:"type"`
	Image        any            `yaml:"image"`
	Command      any            `yaml:"command"` // legacy fixture compatibility
	StartCommand any            `yaml:"startCommand"`
	Schedule     string         `yaml:"schedule"`
	EnvVars      []renderEnvVar `yaml:"envVars"`
}
type renderEnvVar struct {
	Key string `yaml:"key"`
}
type renderDatabase struct {
	Name string `yaml:"name"`
}
type renderCron struct {
	Name     string `yaml:"name"`
	Schedule string `yaml:"schedule"`
}

var renderFileNames = []string{nameRenderYAML, nameRenderYML}

func detectRender(fsys fs.FS) ([]workloadSeed, []Managed, []string, error) {
	body, src, err := readFirstValidFile(fsys, renderFileNames)
	if err != nil || body == nil {
		return nil, nil, nil, err
	}
	var d renderDoc
	if err := yaml.Unmarshal(body, &d); err != nil {
		return nil, nil, nil, fmt.Errorf("reposcan: parse %s: %w", src, err)
	}
	var (
		seeds    []workloadSeed
		managed  []Managed
		warnings []string
	)
	for _, s := range d.Services {
		var cls Class
		switch s.Type {
		case keyWeb:
			cls = ClassHTTP
		case keyWorker:
			cls = ClassWorker
		case "cron":
			cls = ClassJob
		case "pserv", "pserviced":
			cls = ClassServer
		case "keyvalue", "redis":
			if s.Name != "" {
				managed = append(managed, Managed{Name: s.Name, Kind: "redis", EnvHint: hintRedisURL, Source: src + ": " + s.Name})
			}
			continue
		default:
			if s.Name != "" {
				warnings = append(warnings, "reposcan: "+src+": unsupported Render service type "+s.Type+" for "+s.Name+" — skipping")
			}
			continue
		}
		if s.Name == "" {
			continue
		}
		command := s.StartCommand
		if command == nil {
			command = s.Command
		}
		seeds = append(seeds, workloadSeed{
			name:     s.Name,
			class:    cls,
			command:  commandSlice(command),
			envKeys:  renderEnvKeys(s.EnvVars),
			image:    renderImageRef(s.Image),
			schedule: s.Schedule,
			source:   src + ": " + s.Name,
		})
	}
	for _, c := range d.CronJobs {
		if c.Name == "" {
			continue
		}
		seeds = append(seeds, workloadSeed{
			name:     c.Name,
			class:    ClassJob,
			schedule: c.Schedule,
			source:   src + ": " + c.Name,
		})
	}
	for _, database := range d.Databases {
		if database.Name != "" {
			managed = append(managed, Managed{Name: database.Name, Kind: "postgres", EnvHint: hintDatabaseURL, Source: src + ": " + database.Name})
		}
	}
	sort.SliceStable(seeds, func(i, j int) bool { return seeds[i].name < seeds[j].name })
	sort.SliceStable(managed, func(i, j int) bool { return managed[i].Name < managed[j].Name })
	return seeds, managed, warnings, nil
}

func renderImageRef(value any) string {
	switch image := value.(type) {
	case string:
		return image
	case map[string]any:
		if url, ok := image["url"].(string); ok {
			return url
		}
	}
	return ""
}

func renderEnvKeys(entries []renderEnvVar) []string {
	keys := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Key != "" {
			keys = append(keys, entry.Key)
		}
	}
	sort.Strings(keys)
	return keys
}
