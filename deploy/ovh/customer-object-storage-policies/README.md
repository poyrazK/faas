# OVHcloud customer object storage user policies

Customer buckets live in their own Public Cloud project,
`gregale-customer-storage-us-1` (`df46fbb030aa4529980f38f90064cade`), apart
from the platform buckets in `../object-storage-policies/`
(ADR-20261029174627: one project per backend, quota 100 buckets by
default, 1,000 after a support request). The provider registry names the
backend `ovh-bhs-prod-v1`, caps it with `max_buckets`, and sets
`"versioning": "unsupported"`: OVH returns a version ID for every object,
even in an unversioned bucket, and the adapter must read those as the
`"null"` version or no object can be deleted (ADR-20261121150000).

| User | Policy | Used by | Env names in `provider.env` |
|---|---|---|---|
| `gregale-customer-admin` | `gregale-customer-admin.json` | apid (create, delete, CORS, lifecycle), `gregalectl object-storage reconcile` | `GREGALE_OBJECT_STORAGE_ACCESS_KEY`, `GREGALE_OBJECT_STORAGE_SECRET_KEY` |
| `gregale-customer-gateway` | `gregale-customer-gateway.json` | s3-gatewayd, gatewayd-public | `GREGALE_OBJECT_STORAGE_GATEWAY_ACCESS_KEY`, `GREGALE_OBJECT_STORAGE_GATEWAY_SECRET_KEY` |

The gateway key may not create or delete buckets or change their CORS,
policy or ACL (`deploy/ansible/roles/s3_gateway_service/README.md`). OVH
accepts only its own list of S3 actions and has no `*Version*` object
actions: `GetObject` and `DeleteObject` cover versions.
`pkg/daemonunitspec` fails the build if the gateway policy gains a
bucket-admin action, if either policy reaches beyond `gregale-*`, or if
an action is one OVH would refuse.

## Apply

Both users have role ObjectStore operator. Import the policy with the
manager (Object Storage, Users, the user's `...` menu, "Import a user
access policy (JSON)") or `POST /cloud/project/{id}/user/{userId}/policy`
with `{"policy": "<file contents>"}`, then read it back. The keys go into
`/operator/secrets/object-storage.env` on the CD runner host (owner
`faas-runner`, mode 0600) and never into the registry JSON or a log.
