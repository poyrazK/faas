from __future__ import annotations

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
    """Safe original run identity and recorded result only."""

    result: AutomationLiveSuiteObservationResult
    """Result of the original run independently of target drift. Unavailable includes missing runs and terminal
    runs without a current assertion report."""
    run_id: UUID | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        result: str = self.result

        run_id: str | Unset = UNSET
        if not isinstance(self.run_id, Unset):
            run_id = str(self.run_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "result": result,
            }
        )
        if run_id is not UNSET:
            field_dict["run_id"] = run_id

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

        automation_live_suite_observation = cls(
            result=result,
            run_id=run_id,
        )

        return automation_live_suite_observation
