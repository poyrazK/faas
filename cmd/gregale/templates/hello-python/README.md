# hello-python

A minimal Flask hello-world for gregale.

## Deploy

```
gregale deploy --template hello-python --name <slug>
```

The CLI detects `requirements.txt` and selects the `python312` runner.

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
