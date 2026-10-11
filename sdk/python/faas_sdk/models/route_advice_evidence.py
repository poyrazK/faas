from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RouteAdviceEvidence")


@_attrs_define
class RouteAdviceEvidence:
    """Observed traffic for the route over the window. Consumer fields are present only on throttle suggestions."""

    requests: int
    anonymous_requests: int
    server_errors: int
    timeouts: int
    cold_boots: int
    p95_latency_ms: int
    consumers: int | Unset = UNSET
    top_consumer_id: UUID | Unset = UNSET
    top_consumer_requests: int | Unset = UNSET
    top_consumer_peak_per_minute: int | Unset = UNSET
    next_consumer_peak_per_minute: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        requests = self.requests

        anonymous_requests = self.anonymous_requests

        server_errors = self.server_errors

        timeouts = self.timeouts

        cold_boots = self.cold_boots

        p95_latency_ms = self.p95_latency_ms

        consumers = self.consumers

        top_consumer_id: str | Unset = UNSET
        if not isinstance(self.top_consumer_id, Unset):
            top_consumer_id = str(self.top_consumer_id)

        top_consumer_requests = self.top_consumer_requests

        top_consumer_peak_per_minute = self.top_consumer_peak_per_minute

        next_consumer_peak_per_minute = self.next_consumer_peak_per_minute

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "requests": requests,
                "anonymous_requests": anonymous_requests,
                "server_errors": server_errors,
                "timeouts": timeouts,
                "cold_boots": cold_boots,
                "p95_latency_ms": p95_latency_ms,
            }
        )
        if consumers is not UNSET:
            field_dict["consumers"] = consumers
        if top_consumer_id is not UNSET:
            field_dict["top_consumer_id"] = top_consumer_id
        if top_consumer_requests is not UNSET:
            field_dict["top_consumer_requests"] = top_consumer_requests
        if top_consumer_peak_per_minute is not UNSET:
            field_dict["top_consumer_peak_per_minute"] = top_consumer_peak_per_minute
        if next_consumer_peak_per_minute is not UNSET:
            field_dict["next_consumer_peak_per_minute"] = next_consumer_peak_per_minute

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        requests = d.pop("requests")

        anonymous_requests = d.pop("anonymous_requests")

        server_errors = d.pop("server_errors")

        timeouts = d.pop("timeouts")

        cold_boots = d.pop("cold_boots")

        p95_latency_ms = d.pop("p95_latency_ms")

        consumers = d.pop("consumers", UNSET)

        _top_consumer_id = d.pop("top_consumer_id", UNSET)
        top_consumer_id: UUID | Unset
        if isinstance(_top_consumer_id, Unset):
            top_consumer_id = UNSET
        else:
            top_consumer_id = UUID(_top_consumer_id)

        top_consumer_requests = d.pop("top_consumer_requests", UNSET)

        top_consumer_peak_per_minute = d.pop("top_consumer_peak_per_minute", UNSET)

        next_consumer_peak_per_minute = d.pop("next_consumer_peak_per_minute", UNSET)

        route_advice_evidence = cls(
            requests=requests,
            anonymous_requests=anonymous_requests,
            server_errors=server_errors,
            timeouts=timeouts,
            cold_boots=cold_boots,
            p95_latency_ms=p95_latency_ms,
            consumers=consumers,
            top_consumer_id=top_consumer_id,
            top_consumer_requests=top_consumer_requests,
            top_consumer_peak_per_minute=top_consumer_peak_per_minute,
            next_consumer_peak_per_minute=next_consumer_peak_per_minute,
        )

        route_advice_evidence.additional_properties = d
        return route_advice_evidence

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
