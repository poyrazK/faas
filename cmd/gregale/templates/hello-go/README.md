# hello-go

A minimal Go HTTP app for Gregale, using only the standard library. `GET /`
returns a JSON greeting and `GET /healthz` returns `{"ok":true}`.

The template includes a minimal `go.mod` with `go 1.26.10`.

## Deploy

From this directory:

```
gregale start
```

Start signs you in if needed, shows the app name and settings for review,
deploys after you confirm, and tests the first response. Gregale detects
`main.go` and builds with Go 1.26.10.

## Edit and re-deploy

Edit `main.go`, then run `gregale start` again from this directory. It
offers to update the same app and tests the new response. For scripts,
`gregale deploy` updates the same app without prompts.

## Try it

Use the app name (slug) that Start showed:

```
gregale invoke <slug> --method GET --path /    # request your handler
gregale logs <slug> --follow                   # app logs
gregale open <slug>                            # browser
```

On Hobby, Pro and Scale a new app is private: every request needs an API key
as `Authorization: Bearer <key>`, and the browser shows `401 unauthorized`.
`gregale invoke` sends the request with your login, so it is the way to test.
Never put a key in the URL. To make the app public on purpose, run
`gregale app update <slug> --no-require-authn`.

`GET /` returns your greeting and the Go version, as `go_version`; that
response comes from your handler. The public `/healthz` URL answers
`{"status":"ok","source":"edge"}` without a key: Gregale's edge reports that
the route is up, not that your code ran. Your handler's own health check
returns `{"ok":true}`:

```
gregale invoke <slug> --method GET --path /healthz
```

## Run it locally

```
go run .                 # http://localhost:8080
```
