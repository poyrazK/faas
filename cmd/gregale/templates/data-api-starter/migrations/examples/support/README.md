# Read-only support data

Copy `support.sql` into a **new numbered migration** in a project that uses the
`gregale_data_api` binding role. It creates an isolated example dataset; adapt
names and ownership for your application rather than exposing existing private
tables with broad grants. PostgreSQL 15+ is required for security-invoker views.

Keep `support_private` out of `DATA_API_SCHEMAS`. Expose only `api`. The binding
role receives SELECT on safe base columns and no base write privileges. Sensitive
`internal_notes` columns remain ungranted. Security-invoker views preserve those
column grants and base-table RLS; their owners cannot bypass RLS for callers.

Issue signed JWTs with `gregale_read_only: true`, a `tenant` claim, and `sub`.
The gateway rejects POST/PATCH/DELETE (including opted-in RPCs) for these tokens.
It always uses its configured binding role; JWT role claims cannot change it.
Do not reuse broad SELECT grants on these private tables, grant their ownership
to the binding role, or add security-definer RPCs exposing the hidden fields.

For local development, add two identities to `data-api.dev.json`:

```json
"support_a": { "subject": "agent-a", "read_only": true, "claims": { "tenant": "shop-a" } },
"support_b": { "subject": "agent-b", "read_only": true, "claims": { "tenant": "shop-b" } }
```

Add reviewed synthetic fixtures to your seed and GET checks to the required
verification collection. Check tenant isolation, permitted columns, rejected
internal columns, and rejected writes. Open the inspector as `support_a`, choose
`api.support_cases`, select safe columns, set filters/sorting, and save a read-only
support view. Reopening selects its configured read-only identity. Related replies
can be browsed through the exposed FK; export uses the same identity/RLS.

The local inspector capability still grants access to all local test identities;
it is not a production support-staff login or an authorization boundary. Deploy
an application using authenticated support tokens, and review database permissions
and RLS independently before exposing customer data.
