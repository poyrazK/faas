# hello-node

A minimal Express app for Gregale. `GET /` returns a JSON greeting and
`GET /healthz` returns `{"ok":true}`.

## Deploy

From this directory:

```
gregale start
```

Start signs you in if needed, shows the app name and settings for review,
deploys after you confirm, and tests the first response. Gregale detects
`package.json` and runs the app on Node.js 22.

## Edit and re-deploy

Edit `handler.js`, then run `gregale start` again from this directory. It
offers to update the same app and tests the new response. For scripts,
`gregale deploy` updates the same app without prompts.

## Try it

Use the app name (slug) that Start showed:

```
gregale open <slug>             # browser
gregale logs <slug> --follow    # app logs
```

## Add secrets

```
gregale env push --app <slug> -f .env
```

Confirm the keys landed with `gregale secrets list --app <slug>`. It shows
key names only and needs your login. The app's public `/` response does not
list environment variables.

## Run it locally

```
npm install
npm start                # http://localhost:8080
```
