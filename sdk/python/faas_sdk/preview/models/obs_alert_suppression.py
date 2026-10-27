from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_alert_suppression_state import ObsAlertSuppressionState, check_obs_alert_suppression_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsAlertSuppression")


@_attrs_define
class ObsAlertSuppression:
    """Optional suppression field of private platform-alert incident evidence.
    Current suppression requires exact verification of the immutable episode
    against fresh Prometheus rules plus an unambiguous exact-label match in
    Alertmanager. Changed or external labels remain unmatched. StartsAt must
    fall within the current activation at millisecond wire precision and now;
    updatedAt must be fresh and
    endsAt in the future. Resolved episodes are historical and not applicable.
    Silenced and inhibited booleans are null unless current state is verified.
    Suppression is independent of operator triage snooze and delivery receipt.

    """

    state: ObsAlertSuppressionState
    checked_at: datetime.datetime
    silenced: bool | None
    inhibited: bool | None
    reason: str | Unset = UNSET
    updated_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        checked_at = self.checked_at.isoformat()

        silenced: bool | None
        silenced = self.silenced

        inhibited: bool | None
        inhibited = self.inhibited

        reason = self.reason

        updated_at: str | Unset = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
                "checked_at": checked_at,
                "silenced": silenced,
                "inhibited": inhibited,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        state = check_obs_alert_suppression_state(d.pop("state"))

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        def _parse_silenced(data: object) -> bool | None:
            if data is None:
                return data
            return cast(bool | None, data)

        silenced = _parse_silenced(d.pop("silenced"))

        def _parse_inhibited(data: object) -> bool | None:
            if data is None:
                return data
            return cast(bool | None, data)

        inhibited = _parse_inhibited(d.pop("inhibited"))

        reason = d.pop("reason", UNSET)

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: datetime.datetime | Unset
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = datetime.datetime.fromisoformat(_updated_at)

        obs_alert_suppression = cls(
            state=state,
            checked_at=checked_at,
            silenced=silenced,
            inhibited=inhibited,
            reason=reason,
            updated_at=updated_at,
        )

        obs_alert_suppression.additional_properties = d
        return obs_alert_suppression

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
