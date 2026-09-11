# s3-uploader

A minimal port-8080 Node.js app that uploads `POST /upload/`
bodies to an S3-compatible object store. **This is a scaffold, not a
production-ready uploader** — it demonstrates the AWS SDK v3 wiring
and the fail-fast-on-missing-env-var pattern; production apps should
add multipart streaming, retries, and error reporting.

## Managed service

Plug in any S3-compatible provider:

- **AWS S3** — leave `S3_ENDPOINT` unset.
- **Cloudflare R2** — set `S3_ENDPOINT=https://<account>.r2.cloudflarestorage.com`.
- **Backblaze B2** — set `S3_ENDPOINT=https://s3.<region>.backblazeb2.com`.

## First deploy with secrets

Create the file outside this directory so it is not uploaded with the
source, restrict it to your user, and deploy. Gregale creates the app,
seals the secrets, and only then starts the runtime:

```sh
cat > ../s3-uploader.secrets <<'EOF'
S3_BUCKET=my-bucket
S3_REGION=us-east-1
S3_ACCESS_KEY_ID=...
S3_SECRET_ACCESS_KEY=...
# Optional for R2 / B2:
# S3_ENDPOINT=https://<account>.r2.cloudflarestorage.com
EOF
chmod 600 ../s3-uploader.secrets
gregale deploy --secrets-file ../s3-uploader.secrets
```

## Rotate or update secrets

```sh
gregale secrets set --app <slug> S3_BUCKET=my-bucket S3_REGION=us-east-1 S3_ACCESS_KEY_ID=... S3_SECRET_ACCESS_KEY=...
# For R2 / B2, also:
gregale secrets set --app <slug> S3_ENDPOINT=https://<account>.r2.cloudflarestorage.com
```

If any of `S3_BUCKET`, `S3_REGION`, `S3_ACCESS_KEY_ID`,
`S3_SECRET_ACCESS_KEY` are missing, the handler exits at startup with
the exact `gregale secrets set` command — see the `Missing required env
vars` console output.

## Deploy

From this directory:

```sh
gregale deploy
```

## Try it

```sh
gregale open                         # browser, opens the app
curl -X POST --data 'hello world' https://<slug>.gregale.dev/upload/hello.txt
```

The response is `{"ok":true,"key":"hello.txt","bucket":"my-bucket"}`.
Check the bucket in the AWS / R2 / B2 console to confirm the object
landed.

## Re-deploy after edits

Edit `handler.js`, then `gregale deploy` from this directory.
