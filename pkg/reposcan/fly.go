package reposcan

import (
	"fmt"
	"io/fs"
	"sort"

	"github.com/BurntSushi/toml"
)

// fly.toml has a single app + a [processes] section that may name
// per-process types. We read:
//
//	app                       → workload name (the app's name, "name-of-fly-app")
//	[processes]               → per-process names + start commands
//	[http_service]/[[services]] → process groups that serve requests
//
// class is class=http for the app and any service-bound process; other
// process groups are workers. Schedules are not expressible in fly.toml.
//
// BurntSushi/toml reads key `app` into a struct field named `App`
// (case-insensitive match on first letter, exact match on the rest
// via the `toml:"app"` tag), so we expose both a `Name` (legacy
// `name = "…"` form) and an `App` (`app = "…"` form) — Fly v1.x
// has used both names historically and the field that wins is the
// one that wrote the value.
type flyDoc struct {
	App         string            `toml:"app"`
	Name        string            `toml:"name"`
	Processes   map[string]string `toml:"processes"`
	HTTPService struct {
		Processes []string `toml:"processes"`
	} `toml:"http_service"`
	Services []struct {
		Processes []string `toml:"processes"`
	} `toml:"services"`
}

func detectFly(fsys fs.FS) ([]workloadSeed, []Managed, []string, error) {
	body, src, err := readFirstValidFile(fsys, []string{nameFlyTOML})
	if err != nil || body == nil {
		return nil, nil, nil, err
	}
	var d flyDoc
	if err := toml.Unmarshal(body, &d); err != nil {
		return nil, nil, nil, fmt.Errorf("reposcan: parse %s: %w", src, err)
	}
	appName := d.App
	if appName == "" {
		appName = d.Name
	}
	var seeds []workloadSeed
	if appName != "" {
		seeds = append(seeds, workloadSeed{
			name:   appName,
			class:  ClassHTTP,
			source: src + ": " + appName,
		})
	}
	requestServing := make(map[string]bool)
	for _, process := range d.HTTPService.Processes {
		requestServing[process] = true
	}
	for _, service := range d.Services {
		for _, process := range service.Processes {
			requestServing[process] = true
		}
	}
	for pname, command := range d.Processes {
		class := ClassWorker
		if requestServing[pname] || (len(requestServing) == 0 && pname == keyWeb) {
			class = ClassHTTP
		}
		var commandParts []string
		if command != "" {
			commandParts = []string{command}
		}
		seeds = append(seeds, workloadSeed{
			name:    pname,
			class:   class,
			command: commandParts,
			source:  src + ": " + pname,
		})
	}
	sort.SliceStable(seeds, func(i, j int) bool { return seeds[i].name < seeds[j].name })
	return seeds, nil, nil, nil
}
