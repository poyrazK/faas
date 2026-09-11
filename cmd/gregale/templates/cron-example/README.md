# cron-example

A trivial handler designed to be hit by a scheduled synthetic POST.

## Deploy

```
gregale deploy --template cron-example --name <slug>
```

## Schedule it

```
gregale crons add --app <slug> --schedule '*/5 * * * *' --path /
```

Every 5 minutes gregale will POST `{"ping":"cron"}` (or whatever you
pass via the dashboard's cron editor) at this app and the handler
will respond with a fresh invocation_id.

## Verify

```
gregale logs <slug> --follow
```

You should see a `fired_at` line every 5 minutes.
