# Claimable deploys

An agent can deploy to Gregale before the person it works for has an
account. The agent gets a live URL and a claim link. The person opens the
link, confirms an email address, and the app is theirs on the Free plan.

## How an agent deploys without an account

Run `gregale deploy` with no saved login and no `GREGALE_TOKEN`:

- When a coding agent runs it (Claude Code, Codex, Gemini CLI or Cursor),
  the CLI deploys without an account.
- Anywhere else, add `--anonymous`.
- In CI (`CI` is set), or with `GREGALE_NO_ANONYMOUS_DEPLOY=1`, the CLI never
  does this and fails with "not logged in" instead. So does any other
  script without a login, such as a cron job.

```console
$ gregale deploy --anonymous
Deploying without a Gregale account. A person must claim this app by Thu 27 Nov 10:15 UTC or it is deleted:
  https://api.gregale.dev/claim/3q2+…
…
This app has no owner yet. Give this link to the person you are working for;
they claim the app by opening it and confirming their email.
```

The CLI keeps the session's key in a file of its own, separate from your
login. Until the claim window ends, later `gregale deploy` runs and `gregale
claim` use it whenever no one is logged in; other commands need a login.
Once the app is claimed the key stops working: the CLI forgets it and asks
you to run `gregale login`. `gregale logout` forgets it too. `gregale account claim create` prints a fresh claim link. Earlier links, and
confirmation emails sent through them, stop working.

Other clients call the API directly:

```console
$ curl -X POST https://api.gregale.dev/v1/anonymous/sessions \
    -H 'Content-Type: application/json' -d '{"accept_terms": true}'
```

The response carries `api_key`, `claim_url`, `expires_at` and `limits`.
Use the key as a bearer token.

## What an unclaimed app can do

Until someone claims it, the account:

- has the Free plan's limits: one app and the Free memory size;
- can create, deploy, roll back, restart and delete its app, set
  environment variables and secrets, change the app's public access,
  protocol, size and restart settings, and read logs, status and usage;
- cannot do anything else. That includes project deploys (`gregale deploy
  --project`), egress allow lists, custom domains, managed databases
  or caches, buckets, cron jobs, webhooks, log drains, outbound email, API
  keys, deploy tokens and other credentials, organizations, billing and
  plans.

A refused request answers `403 claim_required`. Unclaimed apps are deleted
72 hours after the deploy.

New anonymous deploys are limited per network and across the platform. Past
a limit they answer `429 anonymous_deploys_unavailable`; sign up with
`gregale login` and deploy again.

## Claiming

1. Open the claim link. It lists the apps and when they will be deleted.
2. Enter your email address and open the confirmation link we send. It is
   valid for an hour, and never past the claim window.
3. Select **Claim and sign in**. The account and its apps become yours, and
   you are signed in to the dashboard.

After the claim, the agent's anonymous key stops working. To let the agent
keep deploying, sign in where it runs with `gregale login`.

An address that already has a Gregale account cannot claim: an anonymous
deploy can only become a new account. Claim with another address, or sign
in with `gregale login` and deploy again so the app is created in your
account.
