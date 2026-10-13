from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="EdgeRuleBudgetAction")


@_attrs_define
class EdgeRuleBudgetAction:
    """Per-route end-to-end request budget (ADR-093 / §4.1.2.16).
    The primitive for "POST /payment → 3 s, POST /signup → 10 s"
    without writing timeout-propagation code in the customer's
    app. The hot-path applier (cmd/gatewayd-public/main.go:PR-B
    + pkg/gateway/handler_apply_edge_rule_budget.go) installs a
    per-request `Budget` onto `r.Context()` via
    `reqbudget.WithRemaining`; every downstream hop (DB, gRPC,
    HTTP) tightens itself against the budget via
    `reqbudget.WithOverhead` / `WithCeiling`.

    On expiry the platform returns 504 + RFC 7807 problem
    envelope (`code: request_budget_exceeded`) BEFORE the
    customer's handler runs — the goal is bounded resource
    pin per request, not customer-visible timer logic. Customer
    code can read `reqbudget.FromContext(r.Context()).Remaining`
    if it wants to short-circuit its own work early.

    Field-by-field:
      * `budget_ms` — required wall-clock budget. Must be > 0
        and ≤ 15 min (`RequestBudgetPlanMax`). The plan's ceiling
        (`Plan.RequestBudgetMaxMs`: Free 30 s, Hobby 60 s, Pro
        5 min, Scale 15 min; ADR-1150) clamps it at request time.
        A 0 or negative value is rejected at create-time with 422.
      * `allow_override_header` — optional HTTP request header
        that lets a customer set a per-request override
        (default `x-faas-budget-ms`). Empty (default) =
        no override accepted; the edge-rule value is the
        authoritative budget. The header is parsed as a
        decimal integer in `[1, math.MaxInt32]`; out-of-range
        values are ignored and the edge-rule value wins
        (silent clamp, no 400 — the budget is a quality
        primitive, not a security gate).

    Rejections never reach the wake gate, the auth chain, or
    the rate limiter — same posture as the other kind=budget
    edge-rule appliers. Free-and-above (no plan gate).

    """

    budget_ms: int
    """Per-request wall-clock budget in milliseconds (1 ms – 15 min; the plan's ceiling applies at request time)."""
    allow_override_header: str | Unset = UNSET
    """Optional RFC 7230 token header name for per-request override (default `x-faas-budget-ms`). Empty = no
    override."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        budget_ms = self.budget_ms

        allow_override_header = self.allow_override_header

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "budget_ms": budget_ms,
            }
        )
        if allow_override_header is not UNSET:
            field_dict["allow_override_header"] = allow_override_header

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        budget_ms = d.pop("budget_ms")

        allow_override_header = d.pop("allow_override_header", UNSET)

        edge_rule_budget_action = cls(
            budget_ms=budget_ms,
            allow_override_header=allow_override_header,
        )

        edge_rule_budget_action.additional_properties = d
        return edge_rule_budget_action

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
