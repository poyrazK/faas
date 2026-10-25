from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.automation_live_suite_observation_result import (
    AutomationLiveSuiteObservationResult,
    check_automation_live_suite_observation_result,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="AutomationLiveSuiteObservation")


@_attrs_define
class AutomationLiveSuiteObservation:
    """Safe original run identity, creation time and recorded result only."""

    result: AutomationLiveSuiteObservationResult
    """Result of the original run independently of target drift. Unavailable includes missing runs and terminal
    runs without a current assertion report."""
    run_id: UUID | Unset = UNSET
    created_at: datetime.datetime | Unset = UNSET
    """Original linked verification creation time, when retained. Never refreshed by grouping or checking runs."""

    def to_dict(self) -> dict[str, Any]:
        result: str = self.result

        run_id: str | Unset = UNSET
        if not isinstance(self.run_id, Unset):
            run_id = str(self.run_id)

        created_at: str | Unset = UNSET
        if not isinstance(self.created_at, Unset):
            created_at = self.created_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "result": result,
            }
        )
        if run_id is not UNSET:
            field_dict["run_id"] = run_id
        if created_at is not UNSET:
            field_dict["created_at"] = created_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        result = check_automation_live_suite_observation_result(d.pop("result"))

        _run_id = d.pop("run_id", UNSET)
        run_id: UUID | Unset
        if isinstance(_run_id, Unset):
            run_id = UNSET
        else:
            run_id = UUID(_run_id)

        _created_at = d.pop("created_at", UNSET)
        created_at: datetime.datetime | Unset
        if isinstance(_created_at, Unset):
            created_at = UNSET
        else:
            created_at = datetime.datetime.fromisoformat(_created_at)

        automation_live_suite_observation = cls(
            result=result,
            run_id=run_id,
            created_at=created_at,
        )

        return automation_live_suite_observation
