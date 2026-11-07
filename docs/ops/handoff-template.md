# On-call handoff

Copy this into the on-call channel or the handoff note at the end of a
rotation. The incoming responder replies "acknowledged" once they have read
it and can reach the Pushover target. Never paste credentials, tokens or
customer payloads.

```text
Handoff: <outgoing> -> <incoming>, <YYYY-MM-DD HH:MM UTC>

Pages this rotation:
- <alert>, <count>, <resolved | still firing | silenced until ...>, <runbook used>

Open incidents:
- <incident link>, SEV<n>, IC <name>, next decision at <UTC>, open risks

Active silences (amtool silence query):
- <matcher>, expires <UTC>, why

Holds in force:
- deploy freeze issue: <link or none>
- break-glass release pause: <on | off>  (gregalectl break-glass status)
- drained compute nodes: <node, reason | none>
- feature flags flipped for containment: <flag, value | none>

Watch next:
- <slow-burning warn alerts, backlog trends, a scheduled maintenance, a provider notice>

Follow-ups filed:
- <ticket>, owner

Paging check:
- incoming received a test page on <device> at <UTC>: yes/no
```

A rotation with nothing to report still gets a handoff that says so, plus the
paging check.
