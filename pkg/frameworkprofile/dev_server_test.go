package frameworkprofile

import (
	"testing"
	"testing/fstest"
)

// adr: 1170 — watch mode picks a development server per framework and
// package manager, and nothing when it cannot tell.
func TestDevServerCommand(t *testing.T) {
	file := func(body string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(body)} }
	for _, tc := range []struct {
		name  string
		files fstest.MapFS
		want  string
	}{
		{name: "npm dev script", files: fstest.MapFS{"package.json": file(`{"scripts":{"dev":"vite"}}`)}, want: "npm run dev"},
		{name: "pnpm lockfile", files: fstest.MapFS{"package.json": file(`{"scripts":{"dev":"next dev"}}`), "pnpm-lock.yaml": file("")}, want: "pnpm run dev"},
		{name: "yarn lockfile", files: fstest.MapFS{"package.json": file(`{"scripts":{"dev":"astro dev"}}`), "yarn.lock": file("")}, want: "yarn run dev"},
		{name: "bun lockfile", files: fstest.MapFS{"package.json": file(`{"scripts":{"dev":"bun --hot index.ts"}}`), "bun.lock": file("")}, want: "bun run dev"},
		{name: "packageManager field wins", files: fstest.MapFS{"package.json": file(`{"packageManager":"pnpm@9.12.0","scripts":{"dev":"vite"}}`), "package-lock.json": file("")}, want: "pnpm run dev"},
		{name: "next without dev script", files: fstest.MapFS{"package.json": file(`{"dependencies":{"next":"16.0.0","react":"19.0.0"}}`)}, want: "next dev"},
		{name: "nuxt without dev script", files: fstest.MapFS{"package.json": file(`{"dependencies":{"nuxt":"^4","vue":"^3"}}`)}, want: "nuxt dev"},
		{name: "astro without dev script", files: fstest.MapFS{"package.json": file(`{"dependencies":{"astro":"^5"}}`)}, want: "astro dev"},
		{name: "sveltekit without dev script", files: fstest.MapFS{"package.json": file(`{"devDependencies":{"@sveltejs/kit":"^2","vite":"^6"}}`)}, want: "vite dev"},
		{name: "react router without dev script", files: fstest.MapFS{"package.json": file(`{"devDependencies":{"@react-router/dev":"^7","vite":"^6"}}`)}, want: "react-router dev"},
		{name: "remix with vite", files: fstest.MapFS{"package.json": file(`{"devDependencies":{"@remix-run/dev":"^2","vite":"^5"}}`)}, want: "remix vite:dev"},
		{name: "classic remix", files: fstest.MapFS{"package.json": file(`{"devDependencies":{"@remix-run/dev":"^2"}}`)}},
		{name: "plain vite", files: fstest.MapFS{"package.json": file(`{"devDependencies":{"vite":"^6"}}`)}, want: "vite"},
		{name: "express only", files: fstest.MapFS{"package.json": file(`{"dependencies":{"express":"^5"}}`)}},
		{name: "invalid package.json", files: fstest.MapFS{"package.json": file(`{`)}},
		{name: "django", files: fstest.MapFS{"requirements.txt": file("Django==5.1\n"), "manage.py": file("")}, want: "python manage.py runserver"},
		{name: "fastapi", files: fstest.MapFS{"pyproject.toml": file("[project]\ndependencies = [\"fastapi\", \"uvicorn\"]\n"), "uv.lock": file(""),
			"main.py": file("from fastapi import FastAPI\napi = FastAPI()\n")}, want: "uvicorn main:api --reload"},
		{name: "fastapi in a package", files: fstest.MapFS{"requirements.txt": file("fastapi\nuvicorn\n"), "app/main.py": file("app = FastAPI(title='x')\n")}, want: "uvicorn app.main:app --reload"},
		{name: "flask", files: fstest.MapFS{"requirements.txt": file("flask>=3\n"), "app.py": file("from flask import Flask\napp = Flask(__name__)\n")}, want: "flask --app app:app run --debug"},
		{name: "flask without an app object", files: fstest.MapFS{"requirements.txt": file("flask\n"), "app.py": file("print('hi')\n")}},
		{name: "plain python", files: fstest.MapFS{"requirements.txt": file("requests\n"), "app.py": file("print('hi')\n")}},
		{name: "go", files: fstest.MapFS{"go.mod": file("module x\n")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := DevServerCommand(tc.files)
			if ok != (tc.want != "") || got.Command != tc.want {
				t.Fatalf("DevServerCommand = %+v, %t; want %q", got, ok, tc.want)
			}
			if ok && got.Framework == "" {
				t.Fatal("no framework name for the message")
			}
		})
	}
}
