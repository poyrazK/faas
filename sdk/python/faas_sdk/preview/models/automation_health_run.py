from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.automation_health_run_status import AutomationHealthRunStatus, check_automation_health_run_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="AutomationHealthRun")


@_attrs_define
class AutomationHealthRun:
    """Safe recent-run identity and timestamps; workflow input, output and error text are omitted."""

    id: UUID
    status: AutomationHealthRunStatus
    created_at: datetime.datetime
    finished_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        status: str = self.status

        created_at = self.created_at.isoformat()

        finished_at: str | Unset = UNSET
        if not isinstance(self.finished_at, Unset):
            finished_at = self.finished_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "status": status,
                "created_at": created_at,
            }
        )
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        status = check_automation_health_run_status(d.pop("status"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        _finished_at = d.pop("finished_at", UNSET)
        finished_at: datetime.datetime | Unset
        if isinstance(_finished_at, Unset):
            finished_at = UNSET
        else:
            finished_at = datetime.datetime.fromisoformat(_finished_at)

        automation_health_run = cls(
            id=id,
            status=status,
            created_at=created_at,
            finished_at=finished_at,
        )

        return automation_health_run
