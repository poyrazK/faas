from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.workflow_diagnostic_blocker import WorkflowDiagnosticBlocker


T = TypeVar("T", bound="WorkflowResumePreview")


@_attrs_define
class WorkflowResumePreview:
    """A continuation plan based on the current run generation and recovery admission checks."""

    eligible: bool
    """Advisory eligibility at observation. Actual resume rechecks state and quotas."""
    expected_resume_count: int
    """Observed generation for a subsequent resume request; stale generations are rejected."""
    reopened_steps: list[str]
    """Structurally eligible steps that would reopen even if a temporary admission blocker prevents continuation.
    Empty when recovery planning is unsafe."""
    preserved_steps: list[str]
    """Persisted steps whose current state is retained including completed actions and untaken branches. Sorted by
    name as are reopened_steps and diagnostic steps."""
    blockers: list[WorkflowDiagnosticBlocker]
    """First deterministic planner blocker plus independent admission blockers. Empty only when eligible. Codes
    omit private values."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        eligible = self.eligible

        expected_resume_count = self.expected_resume_count

        reopened_steps = self.reopened_steps

        preserved_steps = self.preserved_steps

        blockers = []
        for blockers_item_data in self.blockers:
            blockers_item = blockers_item_data.to_dict()
            blockers.append(blockers_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "eligible": eligible,
                "expected_resume_count": expected_resume_count,
                "reopened_steps": reopened_steps,
                "preserved_steps": preserved_steps,
                "blockers": blockers,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_diagnostic_blocker import WorkflowDiagnosticBlocker

        d = dict(src_dict)
        eligible = d.pop("eligible")

        expected_resume_count = d.pop("expected_resume_count")

        reopened_steps = cast(list[str], d.pop("reopened_steps"))

        preserved_steps = cast(list[str], d.pop("preserved_steps"))

        blockers = []
        _blockers = d.pop("blockers")
        for blockers_item_data in _blockers:
            blockers_item = WorkflowDiagnosticBlocker.from_dict(blockers_item_data)

            blockers.append(blockers_item)

        workflow_resume_preview = cls(
            eligible=eligible,
            expected_resume_count=expected_resume_count,
            reopened_steps=reopened_steps,
            preserved_steps=preserved_steps,
            blockers=blockers,
        )

        workflow_resume_preview.additional_properties = d
        return workflow_resume_preview

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
