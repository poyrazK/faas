from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectRollbackPreCutoverHealth")


@_attrs_define
class ProjectRollbackPreCutoverHealth:
    """Durable outcome of configured HTTP probes of the prepared revision before cutover."""

    status: str
    """ One of pending, awaiting_probe, passed, or failed. """
    attempt_id: UUID | Unset = UNSET
    checked_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status = self.status

        attempt_id: str | Unset = UNSET
        if not isinstance(self.attempt_id, Unset):
            attempt_id = str(self.attempt_id)

        checked_at: str | Unset = UNSET
        if not isinstance(self.checked_at, Unset):
            checked_at = self.checked_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
            }
        )
        if attempt_id is not UNSET:
            field_dict["attempt_id"] = attempt_id
        if checked_at is not UNSET:
            field_dict["checked_at"] = checked_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = d.pop("status")

        _attempt_id = d.pop("attempt_id", UNSET)
        attempt_id: UUID | Unset
        if isinstance(_attempt_id, Unset):
            attempt_id = UNSET
        else:
            attempt_id = UUID(_attempt_id)

        _checked_at = d.pop("checked_at", UNSET)
        checked_at: datetime.datetime | Unset
        if isinstance(_checked_at, Unset):
            checked_at = UNSET
        else:
            checked_at = datetime.datetime.fromisoformat(_checked_at)

        project_rollback_pre_cutover_health = cls(
            status=status,
            attempt_id=attempt_id,
            checked_at=checked_at,
        )

        project_rollback_pre_cutover_health.additional_properties = d
        return project_rollback_pre_cutover_health

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
