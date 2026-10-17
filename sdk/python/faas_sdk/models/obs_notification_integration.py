from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_notification_integration_integration import (
    ObsNotificationIntegrationIntegration,
    check_obs_notification_integration_integration,
)

if TYPE_CHECKING:
    from ..models.obs_notification_signal import ObsNotificationSignal


T = TypeVar("T", bound="ObsNotificationIntegration")


@_attrs_define
class ObsNotificationIntegration:
    """Attempts and failures for a reviewed integration type across all receivers. Counter presence does not establish
    configuration; receiver names and addresses are omitted.

    """

    integration: ObsNotificationIntegrationIntegration
    attempts: ObsNotificationSignal
    """A usable value requires healthy evidence; null distinguishes missing or stale evidence from a fresh zero.
    Observed time comes from the oldest underlying last-observed sample across matching series."""
    failures: ObsNotificationSignal
    """A usable value requires healthy evidence; null distinguishes missing or stale evidence from a fresh zero.
    Observed time comes from the oldest underlying last-observed sample across matching series."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        integration: str = self.integration

        attempts = self.attempts.to_dict()

        failures = self.failures.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "integration": integration,
                "attempts": attempts,
                "failures": failures,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_notification_signal import ObsNotificationSignal

        d = dict(src_dict)
        integration = check_obs_notification_integration_integration(d.pop("integration"))

        attempts = ObsNotificationSignal.from_dict(d.pop("attempts"))

        failures = ObsNotificationSignal.from_dict(d.pop("failures"))

        obs_notification_integration = cls(
            integration=integration,
            attempts=attempts,
            failures=failures,
        )

        obs_notification_integration.additional_properties = d
        return obs_notification_integration

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
