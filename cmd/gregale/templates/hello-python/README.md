# hello-python

A minimal Flask hello-world for gregale, served by gunicorn (`Procfile`).
Run `python handler.py` for a local development server.

## Deploy

```
gregale deploy --template hello-python --name <slug>
```

The CLI detects `requirements.txt`; the build uses the image's current Python 3.

## Try it

```
gregale open <slug>      # browser
```

## Edit and re-deploy

`--template` creates a fresh copy on every run, so use an initialized
directory when you want to keep edits:

```
gregale init --template hello-python --path hello-python
cd hello-python
# edit handler.py, then:
gregale deploy --name <slug>
```
