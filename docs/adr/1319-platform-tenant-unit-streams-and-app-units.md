# ADR-1319: Customer-wide cards price several units, including app-reported ones

- **Status:** accepted
- **Date:** 2026-10-17
- **Decision:** A platform tenant's rate cards of different units are parallel price histories (streams), as an app's are (ADR-1317), and a tenant card can price the units an app reports (`app:<meter>`, ADR-1318).
  - **Streams.** Tenant cards are grouped by unit. Each stream prices its unit across every subject attributed to the tenant (consumers, surfaces and JWT rules) from its first card, with its own included units, tiers, volume and route weights (route weights on request cards only). Cards of one unit supersede each other by `effective_from`; two cards of one unit cannot share a minute, cards of different units can.
  - **Statements.** A month with a non-request stream in force is re-priced whole (ADR-975, ADR-1310). Each subject minute gets one line per stream that prices it; lines name their card, so the stream is the card's unit. Coverage stays in raw request units. A minute before the tenant's first card of any unit falls back to flat app cards, as before.
  - **App-reported units.** The gateway attaches a request's `Gregale-Usage` units to tenant surface and JWT-rule requests as well as consumer requests. apid records them per tenant subject, meter and minute (`platform_tenant_meter_usage_minutes`) in the same transaction as the request's usage. A stream for `app:<meter>` prices each subject minute's reported units; a minute without a report counts zero.
  - **Plans.** Assigning a tenant plan (ADR-1307) starts its card at the next minute and, for every other unit the tenant is charged for, a free card at the same minute, so the plan stays the tenant's whole price. ADR-1310's rule that a plan of another unit waits for the next month no longer applies.
  - **Exports.** Tenant invoice items name their unit as app invoice items do (ADR-1317).
- **Why:** ADR-1317 and ADR-1318 let an app bill requests, compute and tokens together, but platform tenants, the customers that buy across several apps, could still be priced on one unit. Operators selling a platform to businesses need the same price shapes as operators selling one API.
- **Consequences:**
  - **Changing a unit.** A card of a new unit adds a price rather than replacing the old one; to stop charging a unit, add a card for it priced at zero. A history that changed unit under ADR-1310 now charges both units after the change; add a zero-priced card for the old unit to restore the switch.
  - **Rollout.** apid must run this version before gateways: an older apid rejects usage events that carry reported units for tenant surfaces and JWT rules.
  - **Unchanged.** A tenant whose cards all price requests is quoted exactly as before, including the per-minute path for flat request cards.
- **Rejected alternatives:**
  - Recording app-reported units only for tenant-managed consumers would leave surface and JWT-rule traffic, most of a platform's requests, unmeterable.
  - Keeping the month-boundary unit switch for tenants alone would make the same card behave differently on an app and on a tenant.
