from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="CreateAlertRuleRequestCustomMetricLabels")


@_attrs_define
class CreateAlertRuleRequestCustomMetricLabels:
    """custom_metric rules only: watch the one labelled series with exactly these labels instead of the aggregate (counters
    summed, gauges max across series). Keys match ^[a-z_][a-z0-9_]{0,62}$.

    """

    additional_properties: dict[str, str] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        create_alert_rule_request_custom_metric_labels = cls()

        create_alert_rule_request_custom_metric_labels.additional_properties = d
        return create_alert_rule_request_custom_metric_labels

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> str:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: str) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
