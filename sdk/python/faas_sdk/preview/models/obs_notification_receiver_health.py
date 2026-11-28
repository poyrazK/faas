from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_notification_receiver_health_state import (
    ObsNotificationReceiverHealthState,
    check_obs_notification_receiver_health_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_notification_receiver import ObsNotificationReceiver
    from ..models.obs_notification_signal import ObsNotificationSignal


T = TypeVar("T", bound="ObsNotificationReceiverHealth")


@_attrs_define
class ObsNotificationReceiverHealth:
    """Independent receiver assessment, bounded to 32 receiver/integration groups.
    Deployment intent is independent of actual collector output. Healthy
    requires fresh collection and inventory, complete counter increases and
    an observed successful transport clock for every expected group. Partial
    preserves unavailable evidence, insufficient counter samples and unknown
    success history. Degraded identifies failed requests, missing expected
    transport telemetry or a freshly observed group outside deployment intent.
    This section does not change the top-level aggregate assessment.

    """

    state: ObsNotificationReceiverHealthState
    collector: ObsNotificationSignal
    """A usable value requires healthy evidence; null distinguishes missing or stale evidence from a fresh zero.
    Observed time comes from the oldest underlying last-observed sample across matching series."""
    inventory: ObsNotificationSignal
    """A usable value requires healthy evidence; null distinguishes missing or stale evidence from a fresh zero.
    Observed time comes from the oldest underlying last-observed sample across matching series."""
    groups: list[ObsNotificationReceiver]
    """Reviewed receiver/integration groups ordered by receiver then integration. Addresses, arbitrary names,
    scrape identities and provider errors are excluded."""
    limitations: list[str]
    reason: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        collector = self.collector.to_dict()

        inventory = self.inventory.to_dict()

        groups = []
        for groups_item_data in self.groups:
            groups_item = groups_item_data.to_dict()
            groups.append(groups_item)

        limitations = self.limitations

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
                "collector": collector,
                "inventory": inventory,
                "groups": groups,
                "limitations": limitations,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_notification_receiver import ObsNotificationReceiver
        from ..models.obs_notification_signal import ObsNotificationSignal

        d = dict(src_dict)
        state = check_obs_notification_receiver_health_state(d.pop("state"))

        collector = ObsNotificationSignal.from_dict(d.pop("collector"))

        inventory = ObsNotificationSignal.from_dict(d.pop("inventory"))

        groups = []
        _groups = d.pop("groups")
        for groups_item_data in _groups:
            groups_item = ObsNotificationReceiver.from_dict(groups_item_data)

            groups.append(groups_item)

        limitations = cast(list[str], d.pop("limitations"))

        reason = d.pop("reason", UNSET)

        obs_notification_receiver_health = cls(
            state=state,
            collector=collector,
            inventory=inventory,
            groups=groups,
            limitations=limitations,
            reason=reason,
        )

        obs_notification_receiver_health.additional_properties = d
        return obs_notification_receiver_health

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
