# hello-python

A minimal Flask hello-world for faas.

## Deploy

```
faas deploy --template hello-python
```

imaged will detect `requirements.txt` and use the `python312` runner.

## Try it

```
faas open             # browser, or:
faas curl <slug>      # print first 200 bytes (if available)
```

## Edit and re-deploy

Edit `handler.py`, then re-run `faas deploy --template hello-python --name <slug>`.