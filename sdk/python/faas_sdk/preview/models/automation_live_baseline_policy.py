from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.automation_live_baseline_policy_version import (
    AutomationLiveBaselinePolicyVersion,
    check_automation_live_baseline_policy_version,
)

T = TypeVar("T", bound="AutomationLiveBaselinePolicy")


@_attrs_define
class AutomationLiveBaselinePolicy:
    """Version 2 required-case coverage and evidence-age policy reviewed during baseline promotion."""

    version: AutomationLiveBaselinePolicyVersion
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
        version = check_automation_live_baseline_policy_version(d.pop("version"))

        required_cases = cast(list[str], d.pop("required_cases"))

        max_candidate_age_seconds = d.pop("max_candidate_age_seconds")

        automation_live_baseline_policy = cls(
            version=version,
            required_cases=required_cases,
            max_candidate_age_seconds=max_candidate_age_seconds,
        )

        return automation_live_baseline_policy
