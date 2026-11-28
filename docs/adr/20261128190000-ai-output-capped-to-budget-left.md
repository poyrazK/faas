# ADR-20261128190000 · Calls without an output ceiling are capped to the budget left

- **Status:** accepted
- **Date:** 2026-11-28
- **Amends:** ADR-991 (AI reservations make caps hard), cost-control review
  2026-11-19 N17 (a call naming no output ceiling reserves its model's
  longest response).

## Context

A generation call that names no `max_tokens` reserves its model's longest
response, so concurrent calls cannot each settle far past their reservation
and overshoot a hard cap (N17). That is USD 2.56 a call on opus-5.5 and about
USD 23 on gpt-5.5-pro. The OpenAI SDKs omit `max_tokens` by default, so the
first call of a typical app under an ordinary daily cost cap was refused
(`429 outbound_ai_reservation_exceeds_budget`) with most of the budget
unspent (prod hunt 2026-11-28). The owner chose (2026-11-28) to fix this
before the AI gateway's release.

## Decision

1. When admission refuses a call because its own reservation does not fit a
   token or cost budget (a reason ending in `_reservation`), it also returns
   the room every AI budget leaves after settled usage and calls in flight
   (`Decision.AIBudgetLeft`, both backends).
2. If the call is a generation call whose body names no output ceiling, in a
   format whose ceiling field the gateway can write, the gateway picks the
   largest per-answer ceiling whose whole reservation (every answer, the
   input estimate, tool fees) fits that room, writes it into the body, and
   asks admission once more. The cap is exact: the provider cannot answer
   past it, so the budget stays a hard cap.
3. The ceiling is at least `api.OutboundAIBudgetOutputCapMinTokens` (256);
   below that the original refusal stands. A ceiling the caller named is
   never changed; an app guardrail's ceiling may be lowered further.
4. A capped call carries `X-Gregale-AI-Output-Capped: <tokens>`. Batches and
   realtime sessions are not capped.

## Consequences

- A typical SDK call under a daily cap is answered instead of refused; a
  long answer can stop at the cap (`finish_reason: "length"`), which the
  header explains.
- A capped call's reservation, and so its token-per-minute charge, is its
  ceiling rather than the 4,096-token default; a per-minute guard can still
  refuse it.
- Tag and trace budgets are checked on the second admission as on the first,
  so a capped call can still be refused by them.

## Alternatives

- **Reserve less than the model's longest response.** Lets concurrent calls
  overshoot a hard cap, the N17 finding.
- **Cap every call to the default 4,096 tokens.** Silently truncates answers
  the budget could pay for.
