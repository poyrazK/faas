# hello-go

A minimal Go HTTP app for Gregale, using only the standard library. `GET /`
returns a JSON greeting and `GET /healthz` returns `{"ok":true}`.

The template includes a minimal `go.mod` with `go 1.26.9`.

## Deploy

From this directory:

```
gregale start
```

Start signs you in if needed, shows the app name and settings for review,
deploys after you confirm, and tests the first response. Gregale detects
`main.go` and builds with Go 1.26.9.

## Edit and re-deploy

Edit `main.go`, then run `gregale start` again from this directory. It
offers to update the same app and tests the new response. For scripts,
`gregale deploy` updates the same app without prompts.

## Try it

Use the app name (slug) that Start showed:

```
gregale open <slug>             # browser
gregale logs <slug> --follow    # app logs
```

## Run it locally

```
go run .                 # http://localhost:8080
```
