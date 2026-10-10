from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="TracingConfig")


@_attrs_define
class TracingConfig:
    """Opt-in zero-config request tracing baked into each deployment (ADR-934). Managed runtimes export database, cache and
    HTTP client spans to the debugger without an API key or code changes; apps that configure their own OTel exporter
    are left untouched.

    """

    enabled: bool
    sample_ratio: float | Unset = 1.0
    """Fraction of requests whose spans the app exports."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        sample_ratio = self.sample_ratio

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "enabled": enabled,
            }
        )
        if sample_ratio is not UNSET:
            field_dict["sample_ratio"] = sample_ratio

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        enabled = d.pop("enabled")

        sample_ratio = d.pop("sample_ratio", UNSET)

        tracing_config = cls(
            enabled=enabled,
            sample_ratio=sample_ratio,
        )

        tracing_config.additional_properties = d
        return tracing_config

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
