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
`package.json` and runs the app on Node.js 24: it picks Node.js 24 for any
`engines.node` range that allows it, such as this starter's `>=22`. To run on
Node.js 22, set `engines.node` to `22.x`.

## Edit and re-deploy

Edit `handler.js`, then run `gregale start` again from this directory. It
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

`GET /` returns your greeting and the Node.js version, as `node`; that
response comes from your handler. The public `/healthz` URL answers
`{"status":"ok","source":"edge"}` without a key: Gregale's edge reports that
the route is up, not that your code ran. Your handler's own health check
returns `{"ok":true}`:

```
gregale invoke <slug> --method GET --path /healthz
```

## Add secrets

```
gregale env push --app <slug> -f .env
```

Confirm the keys landed with `gregale env list --app <slug>`. It shows
key names only and needs your login. The app's public `/` response does not
list environment variables.

## Run it locally

```
npm install
npm start                # http://localhost:8080
```
