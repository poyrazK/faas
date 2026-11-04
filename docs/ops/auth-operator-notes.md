# Authentication: operator notes

Installation settings behind the customer [authentication page](../auth.md).
They moved here so that page can be published (#5709).

## GitHub App connection callback

The dashboard's **Connect GitHub** flow uses `/oauth/code-callback`. Register
`https://gregale.dev/oauth/code-callback` as the hosted GitHub App's OAuth
callback URL. This is separate from the dashboard sign-in callback and the
App's installation/setup callback.

For a self-hosted console, configure `cli_auth_url_base` in apid's TOML or
`FAAS_CLI_AUTH_URL_BASE` with the public console URL. GitHub App authorization
defaults to that base plus `/oauth/code-callback`, independently of the API
listener's request host. The console edge must forward this callback to apid.
`FAAS_GITHUB_APP_REDIRECT_URI` takes precedence when a separate callback URL
is required; ensure its value matches the callback registered with GitHub.
