# hello-node

A minimal Express.js hello-world for faas.

## Deploy

From this directory:

```
faas deploy --template hello-node
```

This materializes the template, tars it, and ships it to apid. imaged
will detect `package.json` and use the `node22` runner.

## Try it

```
faas open             # browser, or:
faas curl <slug>      # print first 200 bytes (if available)
```

## Edit and re-deploy

```
# edit handler.js, then:
faas deploy --template hello-node --name <slug>
```

## Add secrets

```
faas env push --app <slug> -f .env
```

The handler's `/` endpoint echoes the secret key names (not values) so
you can confirm the push landed.