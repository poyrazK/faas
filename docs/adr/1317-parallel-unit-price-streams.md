# ADR-1317: Several units priced at once

- **Status:** accepted
- **Date:** 2026-10-15
- **Decision:** An app consumer's cards of different units are parallel price histories (streams), so one consumer can pay for requests, compute seconds, response megabytes and app-reported units (ADR-1318) at the same time.
  - **Streams.** Cards are grouped by unit within the consumer's price history (app default or plan, ADR-953). Each stream prices its unit from its first card, with its own included units, tiers, volume and route weights (route weights on request cards only). Cards in one stream still supersede each other by `effective_from`, and two cards of one unit cannot share a minute.
  - **Statement buckets.** A minute carries one bucket per stream that prices it. Buckets of streams other than request record `unit`, and statements, coverage and adjustments key minutes by minute and unit, so late compute never re-rates requests.
  - **Unpriced minutes.** A minute no stream prices yet is one unpriced request bucket, as before; once any stream prices it, other streams that have not started add nothing for that minute.
  - **One unit stays as it was.** An app whose cards all price one unit is quoted exactly as before streams.
  - **Fees.** Each stream's card in force when the consumer's month starts charges its own base fee and minimum (ADR-1316). A minimum tops up its own stream's usage charge.
  - **Exports.** Invoice items name their unit: requests as "usage units", compute seconds, response megabytes, or the app meter's name.
- **Why:** Common API prices combine dimensions: Cloudflare Workers charges requests and CPU time, inference APIs charge requests and tokens, file APIs charge requests and egress. ADR-1308 let a card price one of them and switched units only at month boundaries.
- **Consequences:**
  - **Changing what is priced.** Adding a card of a new unit adds a price; it no longer replaces the old unit. To stop charging a unit, add a card for it priced at zero. ADR-1308's month-boundary rule for changing units no longer applies to app cards.
  - **More buckets.** A statement can hold several buckets per minute, one per stream.
  - **Customer-wide cards stay single.** Platform tenant cards (ADR-1310) remain one price history, and a change of unit there still starts on the 1st. *Superseded by ADR-1319: tenant cards are streams too.*
- **Rejected alternatives:**
  - One card with several priced components would keep a single timeline, but every component would need its own allowance, tiers and fees inside one card, and changing one price would re-version all of them.
  - Summing units of different kinds into one allowance or ladder would count seconds and requests in one position.
