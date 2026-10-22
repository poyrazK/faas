from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RealtimeConsumerGroupLagAlertConfig")


@_attrs_define
class RealtimeConsumerGroupLagAlertConfig:
    """Realtime Consumer Group Lag Alert Config."""

    enabled: bool
    unassigned_threshold: str | Unset = "0"
    """0 disables backlog threshold; maximum 1000000000."""
    oldest_pending_seconds: int | Unset = 0
    dead_letter_threshold: int | Unset = 0
    cooldown_seconds: int | Unset = 300
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        unassigned_threshold = self.unassigned_threshold

        oldest_pending_seconds = self.oldest_pending_seconds

        dead_letter_threshold = self.dead_letter_threshold

        cooldown_seconds = self.cooldown_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "enabled": enabled,
            }
        )
        if unassigned_threshold is not UNSET:
            field_dict["unassigned_threshold"] = unassigned_threshold
        if oldest_pending_seconds is not UNSET:
            field_dict["oldest_pending_seconds"] = oldest_pending_seconds
        if dead_letter_threshold is not UNSET:
            field_dict["dead_letter_threshold"] = dead_letter_threshold
        if cooldown_seconds is not UNSET:
            field_dict["cooldown_seconds"] = cooldown_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        enabled = d.pop("enabled")

        unassigned_threshold = d.pop("unassigned_threshold", UNSET)

        oldest_pending_seconds = d.pop("oldest_pending_seconds", UNSET)

        dead_letter_threshold = d.pop("dead_letter_threshold", UNSET)

        cooldown_seconds = d.pop("cooldown_seconds", UNSET)

        realtime_consumer_group_lag_alert_config = cls(
            enabled=enabled,
            unassigned_threshold=unassigned_threshold,
            oldest_pending_seconds=oldest_pending_seconds,
            dead_letter_threshold=dead_letter_threshold,
            cooldown_seconds=cooldown_seconds,
        )

        realtime_consumer_group_lag_alert_config.additional_properties = d
        return realtime_consumer_group_lag_alert_config

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
