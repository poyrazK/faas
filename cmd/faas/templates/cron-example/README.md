# cron-example

A trivial handler designed to be hit by a scheduled synthetic POST.

## Deploy

```
faas deploy --template cron-example
```

## Schedule it

```
faas crons add --app <slug> --schedule '*/5 * * * *' --path /
```

Every 5 minutes faas will POST `{"ping":"cron"}` (or whatever you
pass via the dashboard's cron editor) at this app and the handler
will respond with a fresh invocation_id.

## Verify

```
faas logs <slug> --follow
```

You should see a `fired_at` line every 5 minutes.