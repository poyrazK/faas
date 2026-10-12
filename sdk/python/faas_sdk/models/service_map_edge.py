from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ServiceMapEdge")


@_attrs_define
class ServiceMapEdge:
    """Unsampled calls for one caller → target pair. `errors` are final 5xx
    responses after proxy retries; identity and authorization failures
    are excluded. Latency covers successful calls, including routing,
    wake, and forwarding time.

    """

    caller_app_id: str
    caller_app_slug: str
    target_app_id: str
    target_app_slug: str
    calls: int
    errors: int
    error_rate_pct: float
    latency_p50_ms: float
    latency_p95_ms: float
    cold_calls: int
    """Calls that found the target parked and waited for it to restore (ADR-196)."""
    cold_call_pct: float
    """`cold_calls` as a percentage of `calls`."""
    suggest_wake_ahead: bool
    """The caller has not opted into wake-ahead (ADR-977) and its calls
    mostly wake the target: at least 20 cold calls in the window,
    making up at least half of the edge's calls.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        caller_app_id = self.caller_app_id

        caller_app_slug = self.caller_app_slug

        target_app_id = self.target_app_id

        target_app_slug = self.target_app_slug

        calls = self.calls

        errors = self.errors

        error_rate_pct = self.error_rate_pct

        latency_p50_ms = self.latency_p50_ms

        latency_p95_ms = self.latency_p95_ms

        cold_calls = self.cold_calls

        cold_call_pct = self.cold_call_pct

        suggest_wake_ahead = self.suggest_wake_ahead

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "caller_app_id": caller_app_id,
                "caller_app_slug": caller_app_slug,
                "target_app_id": target_app_id,
                "target_app_slug": target_app_slug,
                "calls": calls,
                "errors": errors,
                "error_rate_pct": error_rate_pct,
                "latency_p50_ms": latency_p50_ms,
                "latency_p95_ms": latency_p95_ms,
                "cold_calls": cold_calls,
                "cold_call_pct": cold_call_pct,
                "suggest_wake_ahead": suggest_wake_ahead,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        caller_app_id = d.pop("caller_app_id")

        caller_app_slug = d.pop("caller_app_slug")

        target_app_id = d.pop("target_app_id")

        target_app_slug = d.pop("target_app_slug")

        calls = d.pop("calls")

        errors = d.pop("errors")

        error_rate_pct = d.pop("error_rate_pct")

        latency_p50_ms = d.pop("latency_p50_ms")

        latency_p95_ms = d.pop("latency_p95_ms")

        cold_calls = d.pop("cold_calls")

        cold_call_pct = d.pop("cold_call_pct")

        suggest_wake_ahead = d.pop("suggest_wake_ahead")

        service_map_edge = cls(
            caller_app_id=caller_app_id,
            caller_app_slug=caller_app_slug,
            target_app_id=target_app_id,
            target_app_slug=target_app_slug,
            calls=calls,
            errors=errors,
            error_rate_pct=error_rate_pct,
            latency_p50_ms=latency_p50_ms,
            latency_p95_ms=latency_p95_ms,
            cold_calls=cold_calls,
            cold_call_pct=cold_call_pct,
            suggest_wake_ahead=suggest_wake_ahead,
        )

        service_map_edge.additional_properties = d
        return service_map_edge

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
