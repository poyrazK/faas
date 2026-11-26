---
name: migrate
description: Plan moving an existing app to Gregale from Heroku, Render, Railway, Fly.io or another host, from Gregale's migration guides. Use when the user asks how to move or migrate an app to Gregale.
---

# Move an app to Gregale

Answer from Gregale's docs, not from memory: they say what Gregale reads from
the other host's files and what has no equivalent.

## 1. Learn the app

Ask which host the app runs on now, its language and framework, and what it
uses besides the web process: a database, workers, scheduled jobs, files on
disk, environment variables.

## 2. Read the guide

Call `gregale_search_docs` with "migrate from" and the host's name, then read
the guide with `gregale_get_docs_page`. Guides exist for Heroku, Render,
Railway and Fly.io. For another host, read the framework and container
pages the search returns.

## 3. Map the app

From the guide, tell the user for their app:

- the start command, port and health path Gregale will use;
- where a release or migration command goes;
- how workers and scheduled jobs carry over;
- how to move the database, and that the filesystem is ephemeral, so files
  on local disk must move to a database or object storage.

Link the guide page you used.

## 4. Move the configuration

Settings that are not secret can be set with `gregale_plan_env_change` and
`gregale_apply_env_change`. For each secret, call `gregale_request_secret`
so the user enters the value on a one-time Gregale page. Never ask the user
to paste secrets or an exported environment file into the chat. For many
variables, the guides show how to load them with the Gregale CLI instead.

## 5. Deploy

Deploy a small project with the deploy skill. A larger project deploys from
Codex by upload, or from the user's machine with the Gregale CLI:
`gregale deploy --name my-api`.
