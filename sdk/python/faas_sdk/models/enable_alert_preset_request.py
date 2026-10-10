from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.enable_alert_preset_request_action import (
    EnableAlertPresetRequestAction,
    check_enable_alert_preset_request_action,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EnableAlertPresetRequest")


@_attrs_define
class EnableAlertPresetRequest:
    """Body for POST /v1/apps/{slug}/alert-presets/{name}/enable.
    The (name, metric, comparison, threshold, window_spec,
    default_cooldown_minutes) sextuple is pre-filled from the
    catalog; the caller supplies the delivery-side fields and an
    optional safe-release action.

    """

    webhook_url: str | Unset = UNSET
    """Signed webhook destination; required with webhook_secret unless channel_ids is set."""
    webhook_secret: str | Unset = UNSET
    """HMAC secret for the webhook; required with webhook_url unless channel_ids is set."""
    channel_ids: list[UUID] | Unset = UNSET
    """Notification channels (ADR-749) the instantiated rule delivers to; with at least one, the webhook fields may
    be omitted."""
    action: EnableAlertPresetRequestAction | Unset = "webhook"
    """Action to run when the instantiated alert fires. Omit to
    use the default webhook-only behavior. The login_target_pressure
    and login_target_signal_health presets support webhook only.
    """
    cooldown_minutes: int | Unset = UNSET
    """Override for the preset's default_cooldown_minutes.
    Omit to use the catalog default.
    """
    enabled: bool | Unset = True
    """Whether the instantiated rule is enabled. Defaults to
    true; pass false to stage the rule in disabled state.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        webhook_url = self.webhook_url

        webhook_secret = self.webhook_secret

        channel_ids: list[str] | Unset = UNSET
        if not isinstance(self.channel_ids, Unset):
            channel_ids = []
            for channel_ids_item_data in self.channel_ids:
                channel_ids_item = str(channel_ids_item_data)
                channel_ids.append(channel_ids_item)

        action: str | Unset = UNSET
        if not isinstance(self.action, Unset):
            action = self.action

        cooldown_minutes = self.cooldown_minutes

        enabled = self.enabled

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if webhook_url is not UNSET:
            field_dict["webhook_url"] = webhook_url
        if webhook_secret is not UNSET:
            field_dict["webhook_secret"] = webhook_secret
        if channel_ids is not UNSET:
            field_dict["channel_ids"] = channel_ids
        if action is not UNSET:
            field_dict["action"] = action
        if cooldown_minutes is not UNSET:
            field_dict["cooldown_minutes"] = cooldown_minutes
        if enabled is not UNSET:
            field_dict["enabled"] = enabled

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        webhook_url = d.pop("webhook_url", UNSET)

        webhook_secret = d.pop("webhook_secret", UNSET)

        _channel_ids = d.pop("channel_ids", UNSET)
        channel_ids: list[UUID] | Unset = UNSET
        if _channel_ids is not UNSET:
            channel_ids = []
            for channel_ids_item_data in _channel_ids:
                channel_ids_item = UUID(channel_ids_item_data)

                channel_ids.append(channel_ids_item)

        _action = d.pop("action", UNSET)
        action: EnableAlertPresetRequestAction | Unset
        if isinstance(_action, Unset):
            action = UNSET
        else:
            action = check_enable_alert_preset_request_action(_action)

        cooldown_minutes = d.pop("cooldown_minutes", UNSET)

        enabled = d.pop("enabled", UNSET)

        enable_alert_preset_request = cls(
            webhook_url=webhook_url,
            webhook_secret=webhook_secret,
            channel_ids=channel_ids,
            action=action,
            cooldown_minutes=cooldown_minutes,
            enabled=enabled,
        )

        enable_alert_preset_request.additional_properties = d
        return enable_alert_preset_request

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
