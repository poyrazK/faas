# hello-go

A minimal stdlib-only HTTP handler for gregale.

Gregale supplies a minimal `go.mod` for this template with `go 1.24`.
`gregale init` writes it into your working copy; a direct template deploy
includes the same module in the upload archive.

## Deploy

```
gregale deploy --template hello-go --name <slug>
```

The CLI detects `main.go` and selects the Go 1.24 builder.

## Try it

```
gregale open <slug>      # browser
```

## Edit and re-deploy

`--template` creates a fresh copy on every run, so use an initialized
directory when you want to keep edits:

```
gregale init --template hello-go --path hello-go
cd hello-go
# edit main.go, then:
gregale deploy --name <slug>
```
