from __future__ import annotations

from collections.abc import Mapping
from typing import Any, Literal, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="IssueImpactAlertPolicy")


@_attrs_define
class IssueImpactAlertPolicy:
    """App-level customer-impact threshold measured using verified distinct customers in a rolling 24-hour window."""

    enabled: bool
    minimum_customers: int
    window_seconds: Literal[86400]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        minimum_customers = self.minimum_customers

        window_seconds = self.window_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "enabled": enabled,
                "minimum_customers": minimum_customers,
                "window_seconds": window_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        enabled = d.pop("enabled")

        minimum_customers = d.pop("minimum_customers")

        window_seconds = cast(Literal[86400], d.pop("window_seconds"))
        if window_seconds != 86400:
            raise ValueError(f"window_seconds must match const 86400, got '{window_seconds}'")

        issue_impact_alert_policy = cls(
            enabled=enabled,
            minimum_customers=minimum_customers,
            window_seconds=window_seconds,
        )

        issue_impact_alert_policy.additional_properties = d
        return issue_impact_alert_policy

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
