from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RolloutDecisionSample")


@_attrs_define
class RolloutDecisionSample:
    """One side's aggregates over the observation window. Request counts are organic requests; probe counts are reported
    separately.

    """

    requests: int
    server_errors: int
    probe_requests: int | Unset = UNSET
    probe_server_errors: int | Unset = UNSET
    p95_latency_ms: float | Unset = UNSET
    cold_boot_requests: int | Unset = UNSET
    cold_boot_p95_latency_ms: float | Unset = UNSET
    cpu_requests: int | Unset = UNSET
    cpu_usec: int | Unset = UNSET
    dependency_calls: int | Unset = UNSET
    dependency_errors: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        requests = self.requests

        server_errors = self.server_errors

        probe_requests = self.probe_requests

        probe_server_errors = self.probe_server_errors

        p95_latency_ms = self.p95_latency_ms

        cold_boot_requests = self.cold_boot_requests

        cold_boot_p95_latency_ms = self.cold_boot_p95_latency_ms

        cpu_requests = self.cpu_requests

        cpu_usec = self.cpu_usec

        dependency_calls = self.dependency_calls

        dependency_errors = self.dependency_errors

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "requests": requests,
                "server_errors": server_errors,
            }
        )
        if probe_requests is not UNSET:
            field_dict["probe_requests"] = probe_requests
        if probe_server_errors is not UNSET:
            field_dict["probe_server_errors"] = probe_server_errors
        if p95_latency_ms is not UNSET:
            field_dict["p95_latency_ms"] = p95_latency_ms
        if cold_boot_requests is not UNSET:
            field_dict["cold_boot_requests"] = cold_boot_requests
        if cold_boot_p95_latency_ms is not UNSET:
            field_dict["cold_boot_p95_latency_ms"] = cold_boot_p95_latency_ms
        if cpu_requests is not UNSET:
            field_dict["cpu_requests"] = cpu_requests
        if cpu_usec is not UNSET:
            field_dict["cpu_usec"] = cpu_usec
        if dependency_calls is not UNSET:
            field_dict["dependency_calls"] = dependency_calls
        if dependency_errors is not UNSET:
            field_dict["dependency_errors"] = dependency_errors

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        requests = d.pop("requests")

        server_errors = d.pop("server_errors")

        probe_requests = d.pop("probe_requests", UNSET)

        probe_server_errors = d.pop("probe_server_errors", UNSET)

        p95_latency_ms = d.pop("p95_latency_ms", UNSET)

        cold_boot_requests = d.pop("cold_boot_requests", UNSET)

        cold_boot_p95_latency_ms = d.pop("cold_boot_p95_latency_ms", UNSET)

        cpu_requests = d.pop("cpu_requests", UNSET)

        cpu_usec = d.pop("cpu_usec", UNSET)

        dependency_calls = d.pop("dependency_calls", UNSET)

        dependency_errors = d.pop("dependency_errors", UNSET)

        rollout_decision_sample = cls(
            requests=requests,
            server_errors=server_errors,
            probe_requests=probe_requests,
            probe_server_errors=probe_server_errors,
            p95_latency_ms=p95_latency_ms,
            cold_boot_requests=cold_boot_requests,
            cold_boot_p95_latency_ms=cold_boot_p95_latency_ms,
            cpu_requests=cpu_requests,
            cpu_usec=cpu_usec,
            dependency_calls=dependency_calls,
            dependency_errors=dependency_errors,
        )

        rollout_decision_sample.additional_properties = d
        return rollout_decision_sample

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
