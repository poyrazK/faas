from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.obs_alert_triage_request_action import ObsAlertTriageRequestAction, check_obs_alert_triage_request_action
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsAlertTriageRequest")


@_attrs_define
class ObsAlertTriageRequest:
    """One revision-checked triage action on an exact retained episode ID."""

    action: ObsAlertTriageRequestAction
    expected_revision: int
    snoozed_until: datetime.datetime | Unset = UNSET
    """Required only for snooze; strictly future and at most 24 hours ahead."""

    def to_dict(self) -> dict[str, Any]:
        action: str = self.action

        expected_revision = self.expected_revision

        snoozed_until: str | Unset = UNSET
        if not isinstance(self.snoozed_until, Unset):
            snoozed_until = self.snoozed_until.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "action": action,
                "expected_revision": expected_revision,
            }
        )
        if snoozed_until is not UNSET:
            field_dict["snoozed_until"] = snoozed_until

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        action = check_obs_alert_triage_request_action(d.pop("action"))

        expected_revision = d.pop("expected_revision")

        _snoozed_until = d.pop("snoozed_until", UNSET)
        snoozed_until: datetime.datetime | Unset
        if isinstance(_snoozed_until, Unset):
            snoozed_until = UNSET
        else:
            snoozed_until = datetime.datetime.fromisoformat(_snoozed_until)

        obs_alert_triage_request = cls(
            action=action,
            expected_revision=expected_revision,
            snoozed_until=snoozed_until,
        )

        return obs_alert_triage_request
