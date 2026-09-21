from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="CustomMetricResponse")


@_attrs_define
class CustomMetricResponse:
    """One stored ADR-202 gauge.

    Example:
        {'name': 'orders_pending', 'value': 1284, 'observed_at': '2026-09-21T12:00:00Z', 'stale': False}

    """

    name: str
    value: float
    observed_at: datetime.datetime
    stale: bool
    """True when this row is older than the freshness window and is therefore NOT driving scaling. Computed server-
    side so the API and the scheduler cannot disagree about which rows count."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        value = self.value

        observed_at = self.observed_at.isoformat()

        stale = self.stale

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "value": value,
                "observed_at": observed_at,
                "stale": stale,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        value = d.pop("value")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        stale = d.pop("stale")

        custom_metric_response = cls(
            name=name,
            value=value,
            observed_at=observed_at,
            stale=stale,
        )

        custom_metric_response.additional_properties = d
        return custom_metric_response

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
