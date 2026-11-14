# Organization owner recovery

Use this runbook when a company organization's owner can no longer act and its
members ask for a new owner. For example, the owner has left the company, their
account is suspended, or they have asked to delete it. An organization has
exactly one owner, and only the owner can transfer ownership, delete the
organization or change its security policy. Without this runbook the
organization stays stuck.

The person asking may be trying to take over a company they do not belong to.
Record everything in the ticket and refuse when in doubt.

Decision: ADR-20261113180000. Related: [account-recovery-lost-factors.md](account-recovery-lost-factors.md).

## 1. Confirm the owner cannot act

```sh
sudo gregalectl accounts 360 --account-id <current owner uuid>
```

Go ahead only when one of these holds:

- the owner's account is suspended or pending deletion;
- the owner confirms in writing, from their account's email address, that the
  new owner should take over;
- the company shows in writing, on its letterhead or from its billing contact,
  that the owner has left.

If the owner can still sign in, ask them to transfer ownership from the
dashboard instead.

## 2. Check the new owner

The new owner must already be an active member of the organization, and their
account must be `active` or `past_due`. The command refuses anyone else. Ask the
organization's billing contact or another admin to confirm the request.

## 3. Make them the owner

```sh
sudo gregalectl accounts make-org-owner --account-id <new owner uuid> \
  --org <org slug> --reason owner_left_<ticket_id> --yes
```

The previous owner becomes an admin. The action is audited as
`org.ownership_assigned_by_operator`, and owners and admins are emailed.
Ask the new owner to review the organization's members and API keys.
