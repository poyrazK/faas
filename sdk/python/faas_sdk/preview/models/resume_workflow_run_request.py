from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="ResumeWorkflowRunRequest")


@_attrs_define
class ResumeWorkflowRunRequest:
    """Optimistic continuation request; send the resume_count from the inspected run."""

    expected_resume_count: int

    def to_dict(self) -> dict[str, Any]:
        expected_resume_count = self.expected_resume_count

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_resume_count": expected_resume_count,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_resume_count = d.pop("expected_resume_count")

        resume_workflow_run_request = cls(
            expected_resume_count=expected_resume_count,
        )

        return resume_workflow_run_request
