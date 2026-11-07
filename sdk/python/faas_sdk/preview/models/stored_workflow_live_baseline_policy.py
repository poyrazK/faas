from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.stored_workflow_live_baseline_policy_version import (
    StoredWorkflowLiveBaselinePolicyVersion,
    check_stored_workflow_live_baseline_policy_version,
)

T = TypeVar("T", bound="StoredWorkflowLiveBaselinePolicy")


@_attrs_define
class StoredWorkflowLiveBaselinePolicy:
    """Version 2 required-case coverage and evidence-age policy reviewed during baseline promotion."""

    version: StoredWorkflowLiveBaselinePolicyVersion
    required_cases: list[str]
    max_candidate_age_seconds: int

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        required_cases = self.required_cases

        max_candidate_age_seconds = self.max_candidate_age_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "version": version,
                "required_cases": required_cases,
                "max_candidate_age_seconds": max_candidate_age_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        version = check_stored_workflow_live_baseline_policy_version(d.pop("version"))

        required_cases = cast(list[str], d.pop("required_cases"))

        max_candidate_age_seconds = d.pop("max_candidate_age_seconds")

        stored_workflow_live_baseline_policy = cls(
            version=version,
            required_cases=required_cases,
            max_candidate_age_seconds=max_candidate_age_seconds,
        )

        return stored_workflow_live_baseline_policy
