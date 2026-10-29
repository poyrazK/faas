from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ScenarioSuppressedWebhookEvents")


@_attrs_define
class ScenarioSuppressedWebhookEvents:
    """Count of ordinary webhook deliveries the capture fence suppressed for one workload and event type. Their payloads
    carry platform-generated identifiers, so only multiplicity compares.

    """

    workload: str
    event: str
    count: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        workload = self.workload

        event = self.event

        count = self.count

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "workload": workload,
                "event": event,
                "count": count,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        workload = d.pop("workload")

        event = d.pop("event")

        count = d.pop("count")

        scenario_suppressed_webhook_events = cls(
            workload=workload,
            event=event,
            count=count,
        )

        scenario_suppressed_webhook_events.additional_properties = d
        return scenario_suppressed_webhook_events

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
