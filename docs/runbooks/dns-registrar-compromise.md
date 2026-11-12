# DNS or registrar compromise

Use this when `gregale.dev`, the apps domain, or a customer-facing record
resolves somewhere we did not put it, or when the registrar or Cloudflare
account may be in someone else's hands: unexpected name server, record or
certificate changes, a registrar "transfer requested" email, or Certificate
Transparency entries for our names that we did not issue.

DNS controls where customers send credentials and traffic, and who can pass
domain validation for certificates. Treat this as SEV1 and follow
[breach-notification.md](breach-notification.md) in parallel.

For a DNS **outage** with no sign of tampering, use
[third-party-outage.md](third-party-outage.md). For edgednsd's own record
writes failing, use [FaasEdgeDNS.md](FaasEdgeDNS.md).

## 1. Confirm (first 10 minutes)

From a workstation, compare what the world sees with what we expect:

```sh
dig +short NS gregale.dev
dig +short NS gregale.dev @a.gtld-servers.net   # TLD delegation
dig +short api.gregale.dev @1.1.1.1
dig +short api.gregale.dev @8.8.8.8
whois gregale.dev | grep -iE 'registrar|status|name server|updated'
```

- Name servers at the TLD are not Cloudflare's: **registrar compromise**, §2.
- Name servers are right but records are wrong: **Cloudflare account or
  token compromise**, §3.
- Check Certificate Transparency (crt.sh, `%.gregale.dev` and the apps
  domain) for certificates issued in the window.

## 2. Registrar compromise

1. Call the registrar's abuse or security line, report a hijack, and ask
   them to lock the domain and restore the previous name servers. Record
   the case ID. This is the only fix; nothing we run can override the
   delegation.
2. Recover the registrar account (password reset to a mailbox the attacker
   does not control, new two-factor). If the account's recovery email is on
   the hijacked domain, say so to the registrar immediately.
3. Re-enable registrar lock and, if offered, registry lock.

## 3. Cloudflare account or token compromise

1. Change the password and two-factor of every Cloudflare member; remove
   unknown members.
2. Revoke API tokens you cannot attribute, and rotate the ones we use,
   starting with edgednsd's (`EDGEDNSD_CF_TOKEN` in the production
   environments; see [release-pipeline-compromise.md](release-pipeline-compromise.md)
   §4). A leaked edgednsd token is the most likely path; it can write the
   records it manages.
3. Read the Cloudflare audit log for the window and restore changed
   records, page rules, Workers and WAF rules. Edge entry records are
   rewritten by edgednsd from the manifest once its token works again
   ([FaasEdgeDNS.md](FaasEdgeDNS.md)).
4. Check that Authenticated Origin Pulls and the origin certificate were
   not changed ([EdgeDirectEntry.md](EdgeDirectEntry.md) §3).

## 4. Limit the damage

While the wrong records are live:

- **Sessions and keys.** Anyone who signed in or sent an API key to the
  wrong host gave it to the attacker. Revoke platform-wide once the records
  are correct again, not before, or users will sign in again on the wrong
  host:

  ```sh
  sudo gregalectl break-glass revoke-credentials --db-env /etc/faas/compute-db.env \
    --kinds sessions,api_keys --yes --reason "SEV1 <ticket>: DNS hijack"
  ```

- **Certificates.** Ask the issuing CA to revoke any certificate for our
  names issued in the window, and add or tighten CAA records
  (`issue` for the CAs we use only).
- **Webhooks and email.** Inbound webhooks (GitHub, Polar) and mail for the
  domain may have gone to the attacker. Rotate webhook secrets
  ([GithubWebhookSecretRotation.md](GithubWebhookSecretRotation.md)) and
  check MX and the sending domain's SPF, DKIM and DMARC records.
- **Customers.** Post on the status page and channels outside the platform:
  which names were affected, for how long, and that they must sign in again
  and rotate API keys used in the window.

## 5. Close

Record the window (first wrong answer to last), the names affected, and
every credential revoked. Post-mortem required.

## Before you need it

- [ ] Registrar lock on, registry lock if the TLD offers it.
- [ ] Registrar and Cloudflare accounts use hardware security keys, and
      their recovery email is not on a domain they control.
- [ ] CAA records allow only the CAs we use.
- [ ] Certificate Transparency monitoring for our domains alerts the
      on-call.
- [ ] The edgednsd Cloudflare token is scoped to the records it writes.
- [ ] Decide whether to prepare a secondary DNS provider for a long
      Cloudflare outage ([third-party-outage.md](third-party-outage.md) §1).
