from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_alert_triage_history_entry_action import (
    ObsAlertTriageHistoryEntryAction,
    check_obs_alert_triage_history_entry_action,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsAlertTriageHistoryEntry")


@_attrs_define
class ObsAlertTriageHistoryEntry:
    """Sanitized post-action workflow flags from a durable receipt without actor or owner identities."""

    id: UUID
    revision: int
    action: ObsAlertTriageHistoryEntryAction
    at: datetime.datetime
    acknowledged: bool
    owned: bool
    snoozed_until: datetime.datetime | Unset = UNSET
    trace_id: str | Unset = UNSET
    trace_path: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        revision = self.revision

        action: str = self.action

        at = self.at.isoformat()

        acknowledged = self.acknowledged

        owned = self.owned

        snoozed_until: str | Unset = UNSET
        if not isinstance(self.snoozed_until, Unset):
            snoozed_until = self.snoozed_until.isoformat()

        trace_id = self.trace_id

        trace_path = self.trace_path

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "revision": revision,
                "action": action,
                "at": at,
                "acknowledged": acknowledged,
                "owned": owned,
            }
        )
        if snoozed_until is not UNSET:
            field_dict["snoozed_until"] = snoozed_until
        if trace_id is not UNSET:
            field_dict["trace_id"] = trace_id
        if trace_path is not UNSET:
            field_dict["trace_path"] = trace_path

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        revision = d.pop("revision")

        action = check_obs_alert_triage_history_entry_action(d.pop("action"))

        at = datetime.datetime.fromisoformat(d.pop("at"))

        acknowledged = d.pop("acknowledged")

        owned = d.pop("owned")

        _snoozed_until = d.pop("snoozed_until", UNSET)
        snoozed_until: datetime.datetime | Unset
        if isinstance(_snoozed_until, Unset):
            snoozed_until = UNSET
        else:
            snoozed_until = datetime.datetime.fromisoformat(_snoozed_until)

        trace_id = d.pop("trace_id", UNSET)

        trace_path = d.pop("trace_path", UNSET)

        obs_alert_triage_history_entry = cls(
            id=id,
            revision=revision,
            action=action,
            at=at,
            acknowledged=acknowledged,
            owned=owned,
            snoozed_until=snoozed_until,
            trace_id=trace_id,
            trace_path=trace_path,
        )

        obs_alert_triage_history_entry.additional_properties = d
        return obs_alert_triage_history_entry

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
