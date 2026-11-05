from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsAlertTriage")


@_attrs_define
class ObsAlertTriage:
    """Human workflow on one retained alert episode, independent of observed alert status."""

    revision: int
    acknowledged: bool
    owned: bool
    snoozed: bool
    as_of: datetime.datetime
    owner_account_id: UUID | Unset = UNSET
    snoozed_until: datetime.datetime | Unset = UNSET
    updated_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        revision = self.revision

        acknowledged = self.acknowledged

        owned = self.owned

        snoozed = self.snoozed

        as_of = self.as_of.isoformat()

        owner_account_id: str | Unset = UNSET
        if not isinstance(self.owner_account_id, Unset):
            owner_account_id = str(self.owner_account_id)

        snoozed_until: str | Unset = UNSET
        if not isinstance(self.snoozed_until, Unset):
            snoozed_until = self.snoozed_until.isoformat()

        updated_at: str | Unset = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "revision": revision,
                "acknowledged": acknowledged,
                "owned": owned,
                "snoozed": snoozed,
                "as_of": as_of,
            }
        )
        if owner_account_id is not UNSET:
            field_dict["owner_account_id"] = owner_account_id
        if snoozed_until is not UNSET:
            field_dict["snoozed_until"] = snoozed_until
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        revision = d.pop("revision")

        acknowledged = d.pop("acknowledged")

        owned = d.pop("owned")

        snoozed = d.pop("snoozed")

        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        _owner_account_id = d.pop("owner_account_id", UNSET)
        owner_account_id: UUID | Unset
        if isinstance(_owner_account_id, Unset):
            owner_account_id = UNSET
        else:
            owner_account_id = UUID(_owner_account_id)

        _snoozed_until = d.pop("snoozed_until", UNSET)
        snoozed_until: datetime.datetime | Unset
        if isinstance(_snoozed_until, Unset):
            snoozed_until = UNSET
        else:
            snoozed_until = datetime.datetime.fromisoformat(_snoozed_until)

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: datetime.datetime | Unset
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = datetime.datetime.fromisoformat(_updated_at)

        obs_alert_triage = cls(
            revision=revision,
            acknowledged=acknowledged,
            owned=owned,
            snoozed=snoozed,
            as_of=as_of,
            owner_account_id=owner_account_id,
            snoozed_until=snoozed_until,
            updated_at=updated_at,
        )

        obs_alert_triage.additional_properties = d
        return obs_alert_triage

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
