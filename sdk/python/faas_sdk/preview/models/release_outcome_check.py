from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.release_outcome_check_task_status import (
    ReleaseOutcomeCheckTaskStatus,
    check_release_outcome_check_task_status,
)

T = TypeVar("T", bound="ReleaseOutcomeCheck")


@_attrs_define
class ReleaseOutcomeCheck:
    """A recorded database check after a release command with an unknown outcome. Later release commands of the scope run
    again.

    """

    deployment_id: UUID
    task_id: UUID
    """The release command's task."""
    task_status: ReleaseOutcomeCheckTaskStatus
    """How the release command ended, without an exit code."""
    checked_by: str
    """The person or API key that recorded the check."""
    note: str
    """What the person recorded about the database."""
    checked_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        task_id = str(self.task_id)

        task_status: str = self.task_status

        checked_by = self.checked_by

        note = self.note

        checked_at = self.checked_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "task_id": task_id,
                "task_status": task_status,
                "checked_by": checked_by,
                "note": note,
                "checked_at": checked_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        task_id = UUID(d.pop("task_id"))

        task_status = check_release_outcome_check_task_status(d.pop("task_status"))

        checked_by = d.pop("checked_by")

        note = d.pop("note")

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        release_outcome_check = cls(
            deployment_id=deployment_id,
            task_id=task_id,
            task_status=task_status,
            checked_by=checked_by,
            note=note,
            checked_at=checked_at,
        )

        release_outcome_check.additional_properties = d
        return release_outcome_check

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
