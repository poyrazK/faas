package frameworkprofile

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

const flaskHelloWorld = "testdata/flask-hello-world"

// The coding-agent eval's deploy-flask-render task (2026-11-17) deployed
// render-examples/flask-hello-world: app.py holds `app = Flask(__name__)`,
// requirements.txt declares Flask and Gunicorn, and there is no Procfile and
// no main.py. `gregale deploy --dry-run` showed `gunicorn app:app`, but the
// guest ran Railpack's `gunicorn --bind 0.0.0.0:${PORT:-8000} main:app` and
// crash-looped on ModuleNotFoundError until liveness_exhausted. The located
// start is now pinned over the image's (ADR-1500 amendment 4), through the
// CLI's directory read and apid's archive read alike.
func TestAnalyzePinsFlaskStartWithoutMainModule(t *testing.T) {
	t.Parallel()
	entries := map[string]string{}
	files, err := os.ReadDir(flaskHelloWorld)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		body, err := os.ReadFile(filepath.Join(flaskHelloWorld, file.Name()))
		if err != nil {
			t.Fatal(err)
		}
		entries["flask-hello-world/"+file.Name()] = string(body)
	}
	analyze := map[string]func(*testing.T) (Profile, error){
		"directory": func(*testing.T) (Profile, error) { return AnalyzeDir(flaskHelloWorld) },
		"archive": func(t *testing.T) (Profile, error) {
			return AnalyzeTarballAtRoot(writeProfileArchive(t, entries), "")
		},
	}
	for name, run := range analyze {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := run(t)
			if err != nil {
				t.Fatal(err)
			}
			if got.Framework != "flask" || got.StartCommand != "gunicorn app:app --bind 0.0.0.0:$PORT" || !got.StartPinned || !got.Inferred || got.StartFromConfig {
				t.Fatalf("profile = %+v, want flask, pinned `gunicorn app:app --bind 0.0.0.0:$PORT`", got)
			}
		})
	}
}

func TestAnalyzePythonStartPinned(t *testing.T) {
	t.Parallel()
	const flaskApp = "from flask import Flask\napp = Flask(__name__)\n"
	tests := []struct {
		name    string
		files   fstest.MapFS
		command string
		pinned  bool
	}{
		{
			name: "fastapi in app.py with uvicorn",
			files: fstest.MapFS{
				"requirements.txt": {Data: []byte("fastapi\nuvicorn[standard]\n")},
				"app.py":           {Data: []byte("from fastapi import FastAPI\napp = FastAPI()\n")},
			},
			command: "uvicorn app:app --host 0.0.0.0 --port $PORT", pinned: true,
		},
		{
			name: "flask object under another name in server.py",
			files: fstest.MapFS{
				"pyproject.toml": {Data: []byte("[project]\ndependencies = [\"flask>=3\", \"gunicorn>=23\"]\n")},
				"server.py":      {Data: []byte("from flask import Flask\napplication = Flask(__name__)\n")},
			},
			command: "gunicorn server:application --bind 0.0.0.0:$PORT", pinned: true,
		},
		{
			name: "main.py defines the app Railpack names",
			files: fstest.MapFS{
				"requirements.txt": {Data: []byte("flask\ngunicorn\n")},
				"main.py":          {Data: []byte(flaskApp)},
			},
			command: "gunicorn main:app --bind 0.0.0.0:$PORT",
		},
		{
			name: "main.py may re-export the app",
			files: fstest.MapFS{
				"requirements.txt": {Data: []byte("flask\ngunicorn\n")},
				"app.py":           {Data: []byte(flaskApp)},
				"main.py":          {Data: []byte("from app import app\n")},
			},
			command: "gunicorn app:app --bind 0.0.0.0:$PORT",
		},
		{
			name: "main package",
			files: fstest.MapFS{
				"requirements.txt": {Data: []byte("flask\ngunicorn\n")},
				"app.py":           {Data: []byte(flaskApp)},
				"main/__init__.py": {Data: []byte("from app import app\n")},
			},
			command: "gunicorn app:app --bind 0.0.0.0:$PORT",
		},
		{
			// Railpack runs `python app.py`; the pinned command's server is
			// not installed.
			name: "no gunicorn",
			files: fstest.MapFS{
				"requirements.txt": {Data: []byte("flask\n# gunicorn in production\n")},
				"app.py":           {Data: []byte(flaskApp)},
			},
			command: "gunicorn app:app --bind 0.0.0.0:$PORT",
		},
		{
			name: "entrypoint guessed",
			files: fstest.MapFS{
				"requirements.txt": {Data: []byte("flask\ngunicorn\n")},
				"app.py":           {Data: []byte("from factory import create_app\n")},
			},
			command: "gunicorn app:app --bind 0.0.0.0:$PORT",
		},
		{
			name: "Procfile web process",
			files: fstest.MapFS{
				"requirements.txt": {Data: []byte("flask\ngunicorn\n")},
				"app.py":           {Data: []byte(flaskApp)},
				"Procfile":         {Data: []byte("web: gunicorn --workers 2 app:app\n")},
			},
			command: "gunicorn --workers 2 app:app",
		},
		{
			name: "railpack.json start command",
			files: fstest.MapFS{
				"requirements.txt": {Data: []byte("flask\ngunicorn\n")},
				"app.py":           {Data: []byte(flaskApp)},
				"Procfile":         {Data: []byte("web: gunicorn app:app\n")},
				"railpack.json":    {Data: []byte(`{"deploy":{"startCommand":"gunicorn -w 4 app:app"}}`)},
			},
			command: "gunicorn -w 4 app:app",
		},
		{
			name: "django",
			files: fstest.MapFS{
				"requirements.txt": {Data: []byte("django\ngunicorn\n")},
				"manage.py":        {Data: []byte("")},
				"site/wsgi.py":     {Data: []byte("from django.core.wsgi import get_wsgi_application\napplication = get_wsgi_application()\n")},
				"site/settings.py": {Data: []byte("WSGI_APPLICATION = 'site.wsgi.application'\n")},
				"site/__init__.py": {Data: []byte("")},
			},
			command: "gunicorn site.wsgi:application --bind 0.0.0.0:$PORT",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := Analyze(tt.files)
			if err != nil {
				t.Fatal(err)
			}
			if got.StartCommand != tt.command || got.StartPinned != tt.pinned {
				t.Fatalf("start = %q pinned %t, want %q pinned %t", got.StartCommand, got.StartPinned, tt.command, tt.pinned)
			}
		})
	}
}

// gregale.yaml's hosting.start already replaces the image's command; it is
// the customer's and is not reported as pinned.
func TestAnalyzeHostingStartIsNotPinned(t *testing.T) {
	t.Parallel()
	got, err := Analyze(fstest.MapFS{
		"requirements.txt": {Data: []byte("flask\ngunicorn\n")},
		"app.py":           {Data: []byte("from flask import Flask\napp = Flask(__name__)\n")},
		"gregale.yaml":     {Data: []byte("hosting:\n  start: gunicorn -w 2 app:app\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.StartCommand != "gunicorn -w 2 app:app" || !got.StartFromConfig || got.StartPinned {
		t.Fatalf("profile = %+v, want hosting.start from config, not pinned", got)
	}
}
