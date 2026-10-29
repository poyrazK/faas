from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.environment_git_ops_run_status import EnvironmentGitOpsRunStatus, check_environment_git_ops_run_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.environment_git_ops_plan import EnvironmentGitOpsPlan
    from ..models.environment_git_ops_run_steps_type_0_item import EnvironmentGitOpsRunStepsType0Item


T = TypeVar("T", bound="EnvironmentGitOpsRun")


@_attrs_define
class EnvironmentGitOpsRun:
    """Fenced reconciliation attempt with its observed plan and resource-step journal."""

    id: str
    source_id: str
    revision_id: str
    generation: int
    status: EnvironmentGitOpsRunStatus
    plan: EnvironmentGitOpsPlan | None
    steps: list[EnvironmentGitOpsRunStepsType0Item] | None
    started_at: datetime.datetime
    error_code: str | Unset = UNSET
    completed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.environment_git_ops_plan import EnvironmentGitOpsPlan

        id = self.id

        source_id = self.source_id

        revision_id = self.revision_id

        generation = self.generation

        status: str = self.status

        plan: dict[str, Any] | None
        if isinstance(self.plan, EnvironmentGitOpsPlan):
            plan = self.plan.to_dict()
        else:
            plan = self.plan

        steps: list[dict[str, Any]] | None
        if isinstance(self.steps, list):
            steps = []
            for steps_type_0_item_data in self.steps:
                steps_type_0_item = steps_type_0_item_data.to_dict()
                steps.append(steps_type_0_item)

        else:
            steps = self.steps

        started_at = self.started_at.isoformat()

        error_code = self.error_code

        completed_at: str | Unset = UNSET
        if not isinstance(self.completed_at, Unset):
            completed_at = self.completed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "source_id": source_id,
                "revision_id": revision_id,
                "generation": generation,
                "status": status,
                "plan": plan,
                "steps": steps,
                "started_at": started_at,
            }
        )
        if error_code is not UNSET:
            field_dict["error_code"] = error_code
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.environment_git_ops_plan import EnvironmentGitOpsPlan
        from ..models.environment_git_ops_run_steps_type_0_item import EnvironmentGitOpsRunStepsType0Item

        d = dict(src_dict)
        id = d.pop("id")

        source_id = d.pop("source_id")

        revision_id = d.pop("revision_id")

        generation = d.pop("generation")

        status = check_environment_git_ops_run_status(d.pop("status"))

        def _parse_plan(data: object) -> EnvironmentGitOpsPlan | None:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                plan_type_0 = EnvironmentGitOpsPlan.from_dict(data)

                return plan_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(EnvironmentGitOpsPlan | None, data)

        plan = _parse_plan(d.pop("plan"))

        def _parse_steps(data: object) -> list[EnvironmentGitOpsRunStepsType0Item] | None:
            if data is None:
                return data
            try:
                if not isinstance(data, list):
                    raise TypeError()
                steps_type_0 = []
                _steps_type_0 = data
                for steps_type_0_item_data in _steps_type_0:
                    steps_type_0_item = EnvironmentGitOpsRunStepsType0Item.from_dict(steps_type_0_item_data)

                    steps_type_0.append(steps_type_0_item)

                return steps_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(list[EnvironmentGitOpsRunStepsType0Item] | None, data)

        steps = _parse_steps(d.pop("steps"))

        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        error_code = d.pop("error_code", UNSET)

        _completed_at = d.pop("completed_at", UNSET)
        completed_at: datetime.datetime | Unset
        if isinstance(_completed_at, Unset):
            completed_at = UNSET
        else:
            completed_at = datetime.datetime.fromisoformat(_completed_at)

        environment_git_ops_run = cls(
            id=id,
            source_id=source_id,
            revision_id=revision_id,
            generation=generation,
            status=status,
            plan=plan,
            steps=steps,
            started_at=started_at,
            error_code=error_code,
            completed_at=completed_at,
        )

        environment_git_ops_run.additional_properties = d
        return environment_git_ops_run

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
