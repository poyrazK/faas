from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, Literal, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_node_probe_notifications_reason import (
    ObsNodeProbeNotificationsReason,
    check_obs_node_probe_notifications_reason,
)
from ..models.obs_node_probe_notifications_state import (
    ObsNodeProbeNotificationsState,
    check_obs_node_probe_notifications_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_serving_probe_inventory import ObsServingProbeInventory
    from ..models.obs_serving_probe_notification import ObsServingProbeNotification


T = TypeVar("T", bound="ObsNodeProbeNotifications")


@_attrs_define
class ObsNodeProbeNotifications:
    """Complete direct submission evidence for a selected node, independent of
    the fleet preview. Expected is null when inventory cannot establish
    intent, false when fresh configured intent excludes the node, and true
    when both channel slots are expected. Available describes evidence
    completeness, not current provider health or external receipt.

    """

    generated_at: datetime.datetime
    scope: Literal["node_serving_probe_notification_submissions"]
    node: str
    state: ObsNodeProbeNotificationsState
    max_age_seconds: Literal[120]
    inventory: ObsServingProbeInventory
    """Configuration and freshness of independent serving-probe intent."""
    expected: bool | None
    max_result_age_seconds: float | None
    channels: list[ObsServingProbeNotification]
    reason: ObsNodeProbeNotificationsReason | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        generated_at = self.generated_at.isoformat()

        scope = self.scope

        node = self.node

        state: str = self.state

        max_age_seconds = self.max_age_seconds

        inventory = self.inventory.to_dict()

        expected: bool | None
        expected = self.expected

        max_result_age_seconds: float | None
        max_result_age_seconds = self.max_result_age_seconds

        channels = []
        for channels_item_data in self.channels:
            channels_item = channels_item_data.to_dict()
            channels.append(channels_item)

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "generated_at": generated_at,
                "scope": scope,
                "node": node,
                "state": state,
                "max_age_seconds": max_age_seconds,
                "inventory": inventory,
                "expected": expected,
                "max_result_age_seconds": max_result_age_seconds,
                "channels": channels,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_serving_probe_inventory import ObsServingProbeInventory
        from ..models.obs_serving_probe_notification import ObsServingProbeNotification

        d = dict(src_dict)
        generated_at = datetime.datetime.fromisoformat(d.pop("generated_at"))

        scope = cast(Literal["node_serving_probe_notification_submissions"], d.pop("scope"))
        if scope != "node_serving_probe_notification_submissions":
            raise ValueError(f"scope must match const 'node_serving_probe_notification_submissions', got '{scope}'")

        node = d.pop("node")

        state = check_obs_node_probe_notifications_state(d.pop("state"))

        max_age_seconds = cast(Literal[120], d.pop("max_age_seconds"))
        if max_age_seconds != 120:
            raise ValueError(f"max_age_seconds must match const 120, got '{max_age_seconds}'")

        inventory = ObsServingProbeInventory.from_dict(d.pop("inventory"))

        def _parse_expected(data: object) -> bool | None:
            if data is None:
                return data
            return cast(bool | None, data)

        expected = _parse_expected(d.pop("expected"))

        def _parse_max_result_age_seconds(data: object) -> float | None:
            if data is None:
                return data
            return cast(float | None, data)

        max_result_age_seconds = _parse_max_result_age_seconds(d.pop("max_result_age_seconds"))

        channels = []
        _channels = d.pop("channels")
        for channels_item_data in _channels:
            channels_item = ObsServingProbeNotification.from_dict(channels_item_data)

            channels.append(channels_item)

        _reason = d.pop("reason", UNSET)
        reason: ObsNodeProbeNotificationsReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_node_probe_notifications_reason(_reason)

        obs_node_probe_notifications = cls(
            generated_at=generated_at,
            scope=scope,
            node=node,
            state=state,
            max_age_seconds=max_age_seconds,
            inventory=inventory,
            expected=expected,
            max_result_age_seconds=max_result_age_seconds,
            channels=channels,
            reason=reason,
        )

        obs_node_probe_notifications.additional_properties = d
        return obs_node_probe_notifications

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
