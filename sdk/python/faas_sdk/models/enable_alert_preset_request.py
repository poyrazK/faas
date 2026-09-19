from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

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

    webhook_url: str
    webhook_secret: str
    action: EnableAlertPresetRequestAction | Unset = "webhook"
    """Action to run when the instantiated alert fires. Omit to
    use the default webhook-only behavior.
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

        action: str | Unset = UNSET
        if not isinstance(self.action, Unset):
            action = self.action

        cooldown_minutes = self.cooldown_minutes

        enabled = self.enabled

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "webhook_url": webhook_url,
                "webhook_secret": webhook_secret,
            }
        )
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
        webhook_url = d.pop("webhook_url")

        webhook_secret = d.pop("webhook_secret")

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
