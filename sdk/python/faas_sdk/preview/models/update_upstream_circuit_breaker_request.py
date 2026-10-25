from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="UpdateUpstreamCircuitBreakerRequest")


@_attrs_define
class UpdateUpstreamCircuitBreakerRequest:
    """Partial update of an upstream's egress circuit-breaker policy
    (ADR-201 §3). Omitted fields are left unchanged.

    Setting `enabled` to false leaves the threshold fields intact, so
    toggling protection off does not discard your tuning.

    """

    enabled: bool | Unset = UNSET
    """Turn egress breaking on or off for this upstream."""
    failure_threshold: float | Unset = UNSET
    min_samples: int | Unset = UNSET
    open_seconds: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        failure_threshold = self.failure_threshold

        min_samples = self.min_samples

        open_seconds = self.open_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if enabled is not UNSET:
            field_dict["enabled"] = enabled
        if failure_threshold is not UNSET:
            field_dict["failure_threshold"] = failure_threshold
        if min_samples is not UNSET:
            field_dict["min_samples"] = min_samples
        if open_seconds is not UNSET:
            field_dict["open_seconds"] = open_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        enabled = d.pop("enabled", UNSET)

        failure_threshold = d.pop("failure_threshold", UNSET)

        min_samples = d.pop("min_samples", UNSET)

        open_seconds = d.pop("open_seconds", UNSET)

        update_upstream_circuit_breaker_request = cls(
            enabled=enabled,
            failure_threshold=failure_threshold,
            min_samples=min_samples,
            open_seconds=open_seconds,
        )

        update_upstream_circuit_breaker_request.additional_properties = d
        return update_upstream_circuit_breaker_request

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
