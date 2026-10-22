# hello-python

A minimal Flask app for Gregale, served by gunicorn (`Procfile`). `GET /`
returns a JSON greeting and `GET /healthz` returns `{"ok":true}`.

## Deploy

From this directory:

```
gregale start
```

Start signs you in if needed, shows the app name and settings for review,
deploys after you confirm, and tests the first response. Gregale detects
`requirements.txt` and builds with the image's current Python 3.

## Edit and re-deploy

Edit `handler.py`, then run `gregale start` again from this directory. It
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
pip install -r requirements.txt
python handler.py        # Flask development server on http://localhost:8080
```
