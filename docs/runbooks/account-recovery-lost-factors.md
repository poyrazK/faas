# Account recovery: lost authenticator and lost mailbox

Use this when a customer asks support to get back into a Gregale account and
the self-serve paths do not work for them. Account recovery is the most common
way accounts are taken over: the person asking may not be the owner. Work
slowly, write everything into the support ticket, and refuse when in doubt.

Decision: ADR-20261108160000.

## 1. Send them to self-serve first

| They still have | Self-serve path |
|---|---|
| The authenticator | Sign in normally. To change a lost mailbox, use **Change email** on the account page; a fresh code confirms it. |
| A recovery code | `gregale account mfa recover`, or **Recover access** on the sign-in code page. |
| The mailbox | **Recover access** → email: the disable link is mailed and works after the 24-hour waiting period. |
| An API key with admin scope | The key keeps working; it can list sessions and keys, but it cannot remove MFA or change the email (those need a browser session). Note it as proof (section 3). |

Only someone who has **none** of the authenticator, a recovery code or the
mailbox goes on to section 2. If they lost only the mailbox, they do not need
support: they sign in with the authenticator and change the email.

## 2. Refuse these

- **Operator accounts.** Use
  [operator-credential-compromise.md](operator-credential-compromise.md).
  `gregalectl accounts reset-mfa` refuses them.
- **Requests under time pressure**, threats, or a story that changes between
  messages. Pressure is the attacker's main tool; the waiting period below is
  not negotiable.
- **Requests that ask to change the sign-in email to a new address.**
  Support never does that. After the reset the owner signs in and changes it
  themselves; the confirmation link goes to the new address and the old one
  gets a notice. The one exception is moving an account *back* after someone
  else changed it: [account-recovery-email-changed.md](account-recovery-email-changed.md).
- **Accounts whose orgs hold other people's data** (any org with more than one
  member) without the second check in section 3.

## 3. Check identity: at least two, from different rows

| Proof | How to check |
|---|---|
| Payment | The requester states the card brand, last four digits and the amount and date of a recent invoice. Compare with the billing provider's customer for the account (`gregalectl accounts 360 --account-id ID`). |
| A linked sign-in method | They sign in with the linked Google or GitHub identity (the first factor still works; the session stops at the code page). `auth.session.created` with that method appears in `gregalectl accounts activity --account-id ID`. |
| Password | The same: a password sign-in reaches the code page. |
| An admin API key | At a time you agree in the ticket, they run `gregale account activity list` with the key; check that request in `gregalectl accounts activity --account-id ID`. |
| DNS | They publish a TXT record you give them (`_gregale-recovery.<their custom domain>` = a random value from the ticket) on a custom domain attached to one of the account's apps. |
| Org co-owner | Another owner of a shared org confirms, from their own signed-in session, in the ticket. Required in addition for accounts in multi-member orgs. |

Record which two proofs you checked and how. Two pieces of public
information (name, company, app names) prove nothing.

## 4. Notify and wait 72 hours

1. Mail the account's current email and, for each shared org, its other
   owners: "Gregale support received a request to turn off two-factor
   authentication on the account <email>. If you did not ask for this, reply
   within 72 hours." Use the ticket system so the mail is recorded.
2. Wait 72 hours. Any objection from the account's address or an org owner
   stops the recovery; escalate to security@.

## 5. Reset

A second operator reviews the ticket, then:

```sh
sudo gregalectl accounts reset-mfa --account-id <account uuid> \
  --reason lost_factors --yes
```

This removes the authenticator and its recovery codes, signs out every
browser, forgets remembered browsers, records `account.mfa_disabled`
(method `operator`) and `operator.action.account_reset_mfa`, and mails the
account's address. API keys keep working; `mfa_required` org policies stay in
force, so the owner may have to enroll again before using the dashboard.

## 6. After the reset

Tell the customer to:

1. sign in (password, magic link or a linked provider);
2. set up two-factor authentication again and store the new recovery codes;
3. if the mailbox is lost, change the email on the account page;
4. review **Security activity**, sessions and API keys on the account page,
   and revoke anything they do not recognise.

Close the ticket with the two proofs, the notification date, both operators
and the reset's trace ID.
