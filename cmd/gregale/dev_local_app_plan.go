package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

type devLocalAppPlan struct {
	command   string
	framework string
}

// Explicit function declarations and conventional handlers retain the existing
// runner path. An invalid function manifest must never fall through to an app.
func planDevLocalWorkload(root, command string) ([]*localFunction, *devLocalAppPlan, error) {
	m, _, err := gregalemanifest.Load(root)
	if err != nil {
		return nil, nil, err
	}
	_, functionSet, err := functionSetManifest(root)
	if err != nil {
		return nil, nil, err
	}
	_, _, function := inferFunctionRuntime(root)
	if functionSet || function || (m != nil && m.Function != nil) {
		if command != "" {
			return nil, nil, errors.New("--watch-command is for HTTP apps; functions use the platform runner")
		}
		if m != nil {
			if err := m.Validate(); err != nil {
				return nil, nil, err
			}
		}
		fns, err := planLocalFunctions(root)
		return fns, nil, err
	}
	app, err := planDevLocalApp(root, command)
	return nil, &app, err
}

func planDevLocalApp(root, command string) (devLocalAppPlan, error) {
	m, _, err := gregalemanifest.Load(root)
	if err != nil {
		return devLocalAppPlan{}, err
	}
	if m != nil && m.Dev != nil {
		if err := m.Dev.Validate(); err != nil {
			return devLocalAppPlan{}, err
		}
		if command == "" && m.Dev.Watch != nil {
			command = m.Dev.Watch.Command
		}
	}
	if command = strings.TrimSpace(command); command != "" {
		if strings.ContainsAny(command, "\r\n\x00") {
			return devLocalAppPlan{}, errors.New("the local app command must be one line without NUL bytes")
		}
		return devLocalAppPlan{command: command, framework: "custom command"}, nil
	}
	profile, err := frameworkprofile.AnalyzeDir(root)
	if err != nil {
		return devLocalAppPlan{}, err
	}
	if server, ok := frameworkprofile.DevServerCommand(os.DirFS(root)); ok {
		return devLocalAppPlan{command: localDevServerCommand(server), framework: server.Framework}, nil
	}
	if profile.StartCommand == "" || (profile.Family != "node" && profile.Family != "python" && profile.Family != "go") {
		return devLocalAppPlan{}, errors.New("no local HTTP app command found; add a dev/start script, set dev.watch.command in gregale.yaml, or pass --watch-command (the server must listen on $PORT)")
	}
	return devLocalAppPlan{command: profile.StartCommand, framework: profile.Framework}, nil
}

// Framework servers do not all read PORT. Bind detected commands explicitly;
// the CLI owns source reload, so Python does not need a second reloader.
func localDevServerCommand(server frameworkprofile.DevServer) string {
	switch server.Framework {
	case "FastAPI":
		return "python3 -m " + strings.TrimSuffix(server.Command, " --reload") + " --host 127.0.0.1 --port $PORT"
	case "Flask":
		return "python3 -m " + strings.TrimSuffix(server.Command, " --debug") + " --host 127.0.0.1 --port $PORT"
	case "Django":
		return "python3 manage.py runserver 127.0.0.1:$PORT --noreload"
	case "Next.js":
		return server.Command + " --hostname 127.0.0.1 --port $PORT"
	case "Vite", "SvelteKit":
		return server.Command + " --host 127.0.0.1 --port $PORT --strictPort"
	case "Astro", "Nuxt":
		return server.Command + " --host 127.0.0.1 --port $PORT"
	default:
		return server.Command
	}
}

func devLocalAppEnv(root string, env []string, port string) []string {
	var path string
	result := make([]string, 0, len(env)+5)
	for _, kv := range env {
		key, value, _ := strings.Cut(kv, "=")
		switch key {
		case "PATH":
			path = value
		case "PORT", "HOST", "GREGALE_LOCAL":
		default:
			result = append(result, kv)
		}
	}
	// Use installed project tools without downloading packages or changing the
	// user's working tree. Python virtual environments commonly expose python3.
	paths := []string{filepath.Join(root, ".venv", "bin"), filepath.Join(root, "venv", "bin"), filepath.Join(root, "node_modules", ".bin"), path}
	return append(result, "PATH="+strings.Join(paths, string(os.PathListSeparator)), "PORT="+port, "HOST=127.0.0.1", "GREGALE_LOCAL=1", "PYTHONUNBUFFERED=1")
}

func (p devLocalAppPlan) describe() string {
	return fmt.Sprintf("%s: %s", p.framework, p.command)
}
