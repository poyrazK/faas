from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, Literal, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_notification_health_state import ObsNotificationHealthState, check_obs_notification_health_state

if TYPE_CHECKING:
    from ..models.obs_notification_api_status import ObsNotificationAPIStatus
    from ..models.obs_notification_health_signals import ObsNotificationHealthSignals
    from ..models.obs_notification_integration import ObsNotificationIntegration


T = TypeVar("T", bound="ObsNotificationHealth")


@_attrs_define
class ObsNotificationHealth:
    """Current pipeline evidence without per-episode delivery or human receipt claims."""

    generated_at: datetime.datetime
    state: ObsNotificationHealthState
    window_seconds: Literal[600]
    alertmanager_api: ObsNotificationAPIStatus
    """Reachability and bounded validity of the local alert-status API; does not establish notification receiver
    readiness."""
    signals: ObsNotificationHealthSignals
    """Fixed signals for target availability, config reload, queue length and capacity, dropped alerts,
    notification attempts and failures."""
    integrations: list[ObsNotificationIntegration]
    """Observed integration types ordered by name; missing counters remain unknown."""
    limitations: list[str]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        generated_at = self.generated_at.isoformat()

        state: str = self.state

        window_seconds = self.window_seconds

        alertmanager_api = self.alertmanager_api.to_dict()

        signals = self.signals.to_dict()

        integrations = []
        for integrations_item_data in self.integrations:
            integrations_item = integrations_item_data.to_dict()
            integrations.append(integrations_item)

        limitations = self.limitations

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "generated_at": generated_at,
                "state": state,
                "window_seconds": window_seconds,
                "alertmanager_api": alertmanager_api,
                "signals": signals,
                "integrations": integrations,
                "limitations": limitations,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_notification_api_status import ObsNotificationAPIStatus
        from ..models.obs_notification_health_signals import ObsNotificationHealthSignals
        from ..models.obs_notification_integration import ObsNotificationIntegration

        d = dict(src_dict)
        generated_at = datetime.datetime.fromisoformat(d.pop("generated_at"))

        state = check_obs_notification_health_state(d.pop("state"))

        window_seconds = cast(Literal[600], d.pop("window_seconds"))
        if window_seconds != 600:
            raise ValueError(f"window_seconds must match const 600, got '{window_seconds}'")

        alertmanager_api = ObsNotificationAPIStatus.from_dict(d.pop("alertmanager_api"))

        signals = ObsNotificationHealthSignals.from_dict(d.pop("signals"))

        integrations = []
        _integrations = d.pop("integrations")
        for integrations_item_data in _integrations:
            integrations_item = ObsNotificationIntegration.from_dict(integrations_item_data)

            integrations.append(integrations_item)

        limitations = cast(list[str], d.pop("limitations"))

        obs_notification_health = cls(
            generated_at=generated_at,
            state=state,
            window_seconds=window_seconds,
            alertmanager_api=alertmanager_api,
            signals=signals,
            integrations=integrations,
            limitations=limitations,
        )

        obs_notification_health.additional_properties = d
        return obs_notification_health

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
