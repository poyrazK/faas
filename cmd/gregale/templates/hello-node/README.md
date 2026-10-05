# hello-node

A minimal Express.js hello-world for gregale.

## Deploy

From this directory:

```
gregale deploy --template hello-node --name <slug>
```

This materializes the template, tars it, and ships it to apid. The CLI
detects `package.json` and selects the `node22` runner.

## Try it

```
gregale open <slug>      # browser
```

## Edit and re-deploy

```
gregale init --template hello-node --path hello-node
cd hello-node
# edit handler.js, then:
gregale deploy --name <slug>
```

## Add secrets

```
gregale env push --app <slug> -f .env
```

Confirm the keys landed with `gregale secrets list --app <slug>`. It shows
key names only and needs your login. The app's public `/` response does not
list environment variables.
