from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_notification_receiver_integration import (
    ObsNotificationReceiverIntegration,
    check_obs_notification_receiver_integration,
)
from ..models.obs_notification_receiver_receiver import (
    ObsNotificationReceiverReceiver,
    check_obs_notification_receiver_receiver,
)
from ..models.obs_notification_receiver_state import ObsNotificationReceiverState, check_obs_notification_receiver_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_notification_signal import ObsNotificationSignal


T = TypeVar("T", bound="ObsNotificationReceiver")


@_attrs_define
class ObsNotificationReceiver:
    """Completed transport request observations for one reviewed receiver role
    and integration type. Multiple destinations within the group aggregate.
    Signal healthy describes usable evidence, not human acknowledgement.
    Expected is null when independent deployment intent cannot be established.

    """

    receiver: ObsNotificationReceiverReceiver
    integration: ObsNotificationReceiverIntegration
    expected: bool | None
    state: ObsNotificationReceiverState
    requests: ObsNotificationSignal
    """A usable value requires healthy evidence; null distinguishes missing or stale evidence from a fresh zero.
    Observed time comes from the oldest underlying last-observed sample across matching series."""
    failures: ObsNotificationSignal
    """A usable value requires healthy evidence; null distinguishes missing or stale evidence from a fresh zero.
    Observed time comes from the oldest underlying last-observed sample across matching series."""
    last_success_observation: ObsNotificationSignal
    """A usable value requires healthy evidence; null distinguishes missing or stale evidence from a fresh zero.
    Observed time comes from the oldest underlying last-observed sample across matching series."""
    reason: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        receiver: str = self.receiver

        integration: str = self.integration

        expected: bool | None
        expected = self.expected

        state: str = self.state

        requests = self.requests.to_dict()

        failures = self.failures.to_dict()

        last_success_observation = self.last_success_observation.to_dict()

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "receiver": receiver,
                "integration": integration,
                "expected": expected,
                "state": state,
                "requests": requests,
                "failures": failures,
                "last_success_observation": last_success_observation,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_notification_signal import ObsNotificationSignal

        d = dict(src_dict)
        receiver = check_obs_notification_receiver_receiver(d.pop("receiver"))

        integration = check_obs_notification_receiver_integration(d.pop("integration"))

        def _parse_expected(data: object) -> bool | None:
            if data is None:
                return data
            return cast(bool | None, data)

        expected = _parse_expected(d.pop("expected"))

        state = check_obs_notification_receiver_state(d.pop("state"))

        requests = ObsNotificationSignal.from_dict(d.pop("requests"))

        failures = ObsNotificationSignal.from_dict(d.pop("failures"))

        last_success_observation = ObsNotificationSignal.from_dict(d.pop("last_success_observation"))

        reason = d.pop("reason", UNSET)

        obs_notification_receiver = cls(
            receiver=receiver,
            integration=integration,
            expected=expected,
            state=state,
            requests=requests,
            failures=failures,
            last_success_observation=last_success_observation,
            reason=reason,
        )

        obs_notification_receiver.additional_properties = d
        return obs_notification_receiver

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
