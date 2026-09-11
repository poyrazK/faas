# slack-bot

A minimal port-8080 Node.js app that handles Slack Events API
callbacks at `POST /slack/events`. **The signature verification is
real**, not a stub: every request is HMAC-SHA256-checked against the
`SLACK_SIGNING_SECRET` per Slack's `v0` signing scheme, and timestamps
older than 5 minutes are rejected. URL-verification handshakes are
answered automatically so the customer's first Slack-app configuration
lands cleanly.

This is a SCAFFOLD. It logs incoming events to stdout (visible via
`gregale logs <slug>`) but does NOT post replies; uncomment the
`chat.postMessage` block in `handler.js` to enable replies (and set
`SLACK_BOT_TOKEN`).

## Managed service

Slack Events API.

## First deploy with secrets

Create the file outside this directory, restrict it to your user, and
deploy. Gregale creates the app, seals the secrets, and only then starts
the runtime:

```sh
cat > ../slack-bot.secrets <<'EOF'
SLACK_SIGNING_SECRET=<your-signing-secret>
# Optional, only if you uncomment chat.postMessage:
# SLACK_BOT_TOKEN=xoxb-...
EOF
chmod 600 ../slack-bot.secrets
gregale deploy --secrets-file ../slack-bot.secrets
```

## Reserve then configure separately

If you prefer to set secrets through the app API, reserve the app first:

```sh
gregale deploy --create-only --template slack-bot --name <slug>
gregale secrets set --app <slug> SLACK_SIGNING_SECRET=<your-signing-secret> SLACK_BOT_TOKEN=xoxb-...
cd <this-directory> && gregale deploy
```

## Rotate or update secrets

```sh
gregale secrets set --app <slug> SLACK_SIGNING_SECRET=<your-signing-secret>
# Optional, only if you uncomment the chat.postMessage block:
gregale secrets set --app <slug> SLACK_BOT_TOKEN=xoxb-...
```

If `SLACK_SIGNING_SECRET` is missing, the handler exits at startup with
the exact `gregale secrets set` command.

## Deploy

From this directory:

```sh
gregale deploy
```

## Wire Slack to the app

In your Slack app's "Event Subscriptions" page, set the Request URL
to:

```
https://<slug>.gregale.dev/slack/events
```

Slack sends a `url_verification` challenge; the handler responds with
`{ challenge: ... }`. Subscribe to events (e.g. `message.channels`),
invite the bot to a channel, and post a message — it should appear
in `gregale logs <slug>` within seconds.

## Re-deploy after edits

Edit `handler.js`, then `gregale deploy` from this directory.
