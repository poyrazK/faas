from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.automation_live_baseline_policy import AutomationLiveBaselinePolicy


T = TypeVar("T", bound="PromoteAutomationLiveBaselineRequest")


@_attrs_define
class PromoteAutomationLiveBaselineRequest:
    """Explicit target suite, reviewed policy and expected baseline revision for an audited promotion."""

    suite_id: UUID
    expected_revision: int
    expected_version: int
    expected_published_version: int
    expected_deployment_id: UUID
    policy: AutomationLiveBaselinePolicy
    """Version 2 required-case coverage and evidence-age policy reviewed during baseline promotion."""

    def to_dict(self) -> dict[str, Any]:
        suite_id = str(self.suite_id)

        expected_revision = self.expected_revision

        expected_version = self.expected_version

        expected_published_version = self.expected_published_version

        expected_deployment_id = str(self.expected_deployment_id)

        policy = self.policy.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "suite_id": suite_id,
                "expected_revision": expected_revision,
                "expected_version": expected_version,
                "expected_published_version": expected_published_version,
                "expected_deployment_id": expected_deployment_id,
                "policy": policy,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_live_baseline_policy import AutomationLiveBaselinePolicy

        d = dict(src_dict)
        suite_id = UUID(d.pop("suite_id"))

        expected_revision = d.pop("expected_revision")

        expected_version = d.pop("expected_version")

        expected_published_version = d.pop("expected_published_version")

        expected_deployment_id = UUID(d.pop("expected_deployment_id"))

        policy = AutomationLiveBaselinePolicy.from_dict(d.pop("policy"))

        promote_automation_live_baseline_request = cls(
            suite_id=suite_id,
            expected_revision=expected_revision,
            expected_version=expected_version,
            expected_published_version=expected_published_version,
            expected_deployment_id=expected_deployment_id,
            policy=policy,
        )

        return promote_automation_live_baseline_request
