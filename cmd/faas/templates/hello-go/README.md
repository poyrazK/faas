# hello-go

A minimal stdlib-only HTTP handler for faas.

The template ships without a `go.mod` because Go's `//go:embed` (which
the CLI uses to ship these templates as a single binary) refuses to
descend into any directory that contains one. imaged auto-creates a
`go.mod` at build time, so you don't need to add one — just edit
`main.go` and re-deploy.

## Deploy

```
faas deploy --template hello-go
```

imaged will detect `main.go` and use the `go1.22` builder (it adds a
`go.mod` for you on first build).

## Try it

```
faas open             # browser
```

## Edit and re-deploy

Edit `main.go`, then re-run `faas deploy --template hello-go --name <slug>`.