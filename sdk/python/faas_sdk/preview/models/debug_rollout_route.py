from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="DebugRolloutRoute")


@_attrs_define
class DebugRolloutRoute:
    """One route's candidate-vs-stable comparison over the rollout window; counts are represented requests."""

    route: str
    candidate_requests: int
    candidate_server_errors: int
    candidate_p95_ms: int
    stable_requests: int
    stable_server_errors: int
    stable_p95_ms: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        route = self.route

        candidate_requests = self.candidate_requests

        candidate_server_errors = self.candidate_server_errors

        candidate_p95_ms = self.candidate_p95_ms

        stable_requests = self.stable_requests

        stable_server_errors = self.stable_server_errors

        stable_p95_ms = self.stable_p95_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "route": route,
                "candidate_requests": candidate_requests,
                "candidate_server_errors": candidate_server_errors,
                "candidate_p95_ms": candidate_p95_ms,
                "stable_requests": stable_requests,
                "stable_server_errors": stable_server_errors,
                "stable_p95_ms": stable_p95_ms,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        route = d.pop("route")

        candidate_requests = d.pop("candidate_requests")

        candidate_server_errors = d.pop("candidate_server_errors")

        candidate_p95_ms = d.pop("candidate_p95_ms")

        stable_requests = d.pop("stable_requests")

        stable_server_errors = d.pop("stable_server_errors")

        stable_p95_ms = d.pop("stable_p95_ms")

        debug_rollout_route = cls(
            route=route,
            candidate_requests=candidate_requests,
            candidate_server_errors=candidate_server_errors,
            candidate_p95_ms=candidate_p95_ms,
            stable_requests=stable_requests,
            stable_server_errors=stable_server_errors,
            stable_p95_ms=stable_p95_ms,
        )

        debug_rollout_route.additional_properties = d
        return debug_rollout_route

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
