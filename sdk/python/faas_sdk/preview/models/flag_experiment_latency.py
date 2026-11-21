from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="FlagExperimentLatency")


@_attrs_define
class FlagExperimentLatency:
    """One variant's p95 latency, from a histogram with 5% buckets."""

    variant: str
    requests: int
    p95_ms: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        variant = self.variant

        requests = self.requests

        p95_ms = self.p95_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "variant": variant,
                "requests": requests,
                "p95_ms": p95_ms,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        variant = d.pop("variant")

        requests = d.pop("requests")

        p95_ms = d.pop("p95_ms")

        flag_experiment_latency = cls(
            variant=variant,
            requests=requests,
            p95_ms=p95_ms,
        )

        flag_experiment_latency.additional_properties = d
        return flag_experiment_latency

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
