from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="EdgeRuleBudgetAction")


@_attrs_define
class EdgeRuleBudgetAction:
    """Per-route execution budget plus an optional total deadline (ADR-531).
    budget_ms starts after upload, wake and capacity admission. Its
    configured override header (default x-faas-budget-ms) may alter the
    execution allowance within the plan ceiling.

    total_deadline_ms starts when the public proxy receives the request
    and includes upload, routing, auth, queueing, wake, retries and the
    ordinary response exchange. It cannot be increased by an execution
    override. Zero or omission leaves existing execution semantics.
    Plan limits cap both values. Expiry returns a 504 problem with code
    request_budget_exceeded before response commitment; a committed
    ordinary response is terminated on expiry. Streaming and upgrades
    use their idle/session contract after successful response headers.
    Detached work and unmediated guest sockets are excluded. Nested
    managed-service deadline transport remains pending acceptance.

    """

    budget_ms: int
    """Execution allowance in milliseconds, after upload/wake/admission."""
    total_deadline_ms: int | Unset = 0
    """Optional total deadline from trusted public ingress; zero leaves it unset."""
    allow_override_header: str | Unset = UNSET
    """Header for the execution override; default x-faas-budget-ms. It never increases total_deadline_ms."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        budget_ms = self.budget_ms

        total_deadline_ms = self.total_deadline_ms

        allow_override_header = self.allow_override_header

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "budget_ms": budget_ms,
            }
        )
        if total_deadline_ms is not UNSET:
            field_dict["total_deadline_ms"] = total_deadline_ms
        if allow_override_header is not UNSET:
            field_dict["allow_override_header"] = allow_override_header

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        budget_ms = d.pop("budget_ms")

        total_deadline_ms = d.pop("total_deadline_ms", UNSET)

        allow_override_header = d.pop("allow_override_header", UNSET)

        edge_rule_budget_action = cls(
            budget_ms=budget_ms,
            total_deadline_ms=total_deadline_ms,
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
