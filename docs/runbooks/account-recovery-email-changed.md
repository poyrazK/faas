# Account recovery: sign-in email changed by someone else

Use this when a customer says the sign-in email of their Gregale account was
changed to an address they do not control. Once a change is confirmed, every
password reset and sign-in link goes to the new address, so the owner has no
self-serve way back. Support can move the account back to the previous
address within 30 days of the change (`api.EmailChangeRevertWindow`). It can
never move an account to any other address.

The person asking may be the attacker trying to take a sold or shared account
back. Work slowly, record everything in the ticket, and refuse when in doubt.

Decision: ADR-20261110100000. Related: [account-recovery-lost-factors.md](account-recovery-lost-factors.md).

## 1. Confirm the change happened

```sh
sudo gregalectl accounts activity --account-id <account uuid>
```

Look for `account.email_change_requested` and `account.email_changed`. The
previous address received two mails: "Someone asked to change your Gregale
sign-in email" at the request and "Your Gregale sign-in email was changed" at
the confirmation. Ask the requester to forward both from the previous
address, with full headers.

Refuse when:

- there is no `account.email_changed` in the last 30 days, or the account
  changed its email again since (`revert-email` refuses both);
- the account is an operator account (use
  [operator-credential-compromise.md](operator-credential-compromise.md));
- the request is under time pressure or the story changes between messages.

## 2. Check identity: at least two, from different rows

| Proof | How to check |
|---|---|
| The previous mailbox | The forwarded notice mails from section 1, with headers that show delivery to the previous address. Required. |
| Payment | Card brand, last four digits and the amount and date of a recent invoice, compared with `gregalectl accounts 360 --account-id ID`. |
| A linked sign-in method | They sign in with the linked Google or GitHub identity at a time agreed in the ticket; `auth.session.created` appears in the activity. |
| DNS | A TXT record you give them (`_gregale-recovery.<their custom domain>` = a random value from the ticket) on a custom domain attached to one of the account's apps. |
| Org co-owner | Another owner of a shared org confirms from their own signed-in session. Required in addition for accounts in multi-member orgs. |

The previous mailbox plus one other row. Public information proves nothing.

## 3. Notify and wait 24 hours

Mail the **current** (new) address and, for each shared org, its other
owners: "Gregale support received a request to move the account <new
address> back to its previous sign-in email. If you made the change and did
not lose control of the account, reply within 24 hours." Any objection stops
the recovery: escalate to security@.

## 4. Revert

A second operator reviews the ticket, then:

```sh
sudo gregalectl accounts revert-email --account-id <account uuid> \
  --reason email_takeover --yes
```

This moves the account back to the previous address and marks it verified,
spends sign-in, reset and two-factor-disable links mailed to the new address,
signs out every browser, forgets remembered browsers, revokes CLI, MCP and
password sign-in keys, cancels pending email changes, invitations and CLI
approvals, moves billing emails back, and records `account.email_reverted`
and `operator.action.account_revert_email`. The previous address gets a
notice and a list of the API keys that still work.

It answers 409 when there is nothing to revert or another account now uses
the previous address. In the second case, stop and escalate.

## 5. After the revert

Tell the customer to:

1. use **Forgot password** at the previous address to set a new password;
2. if the attacker also turned on two-factor authentication, follow
   [account-recovery-lost-factors.md](account-recovery-lost-factors.md);
3. review **Sign-in methods**, **API keys** and **Security activity** on the
   account page and remove anything they do not recognise.

Close the ticket with the proofs, the notification time, both operators and
the revert's trace ID.
