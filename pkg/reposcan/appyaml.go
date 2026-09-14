package reposcan

import (
	"fmt"
	"io/fs"
	"sort"

	"gopkg.in/yaml.v3"
)

// appYamlDoc is the AppYAML (Snapps-equivalent) format used by a
// few shopify-style PaaS. Top-level keys:
//
//	services:    {name: {command, env, ports}}
//	workers:     {name: {command, env}}   → class=worker
//	jobs:        [{name, schedule, command}] → class=job
//	cron:        list of {name, schedule, command} (legacy)
//
// One source may declare one or more sections; we walk each.
type appYamlDoc struct {
	Services map[string]appYamlService `yaml:"services"`
	Workers  map[string]appYamlWorker  `yaml:"workers"`
	Jobs     []appYamlJob              `yaml:"jobs"`
	Cron     []appYamlJob              `yaml:"cron"`
}
type appYamlService struct {
	Command any            `yaml:"command"`
	Env     map[string]any `yaml:"env"`
	Ports   []any          `yaml:"ports"`
}
type appYamlWorker struct {
	Command any            `yaml:"command"`
	Env     map[string]any `yaml:"env"`
}
type appYamlJob struct {
	Name     string `yaml:"name"`
	Schedule string `yaml:"schedule"`
	Command  any    `yaml:"command"`
}

var appYamlFileNames = []string{nameAppYAML, nameAppYML}

func detectAppYaml(fsys fs.FS) ([]workloadSeed, []Managed, []string, error) {
	body, src, err := readFirstValidFile(fsys, appYamlFileNames)
	if err != nil || body == nil {
		return nil, nil, nil, err
	}
	var d appYamlDoc
	if err := yaml.Unmarshal(body, &d); err != nil {
		return nil, nil, nil, fmt.Errorf("reposcan: parse %s: %w", src, err)
	}
	var seeds []workloadSeed
	for n, s := range d.Services {
		if n == "" {
			continue
		}
		seeds = append(seeds, workloadSeed{
			name:    n,
			class:   ClassHTTP,
			command: commandSlice(s.Command),
			envKeys: envKeys(s.Env),
			ports:   parsePorts(s.Ports),
			source:  src + ": services." + n,
		})
	}
	for n, w := range d.Workers {
		if n == "" {
			continue
		}
		seeds = append(seeds, workloadSeed{
			name:    n,
			class:   ClassWorker,
			command: commandSlice(w.Command),
			envKeys: envKeys(w.Env),
			source:  src + ": workers." + n,
		})
	}
	all := append([]appYamlJob{}, d.Jobs...)
	all = append(all, d.Cron...)
	for _, j := range all {
		if j.Name == "" {
			continue
		}
		seeds = append(seeds, workloadSeed{
			name:     j.Name,
			class:    ClassJob,
			schedule: j.Schedule,
			command:  commandSlice(j.Command),
			source:   src + ": jobs." + j.Name,
		})
	}
	sort.SliceStable(seeds, func(i, j int) bool { return seeds[i].name < seeds[j].name })
	return seeds, nil, nil, nil
}
