from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_recovery_policy import WorkflowRecoveryPolicy


T = TypeVar("T", bound="WorkflowRecoveryPolicyEvaluationRequest")


@_attrs_define
class WorkflowRecoveryPolicyEvaluationRequest:
    """Proposed policy and bounded historical review window, fenced against the current configuration version."""

    expected_version: int
    policy: WorkflowRecoveryPolicy
    """Explicit app recovery policy. Preview prepares parent-job plans and worker-discovered initial-failure
    reviews. initial_failure requires explicit failure statuses. Opt-in automatic_retry requires saved_failure only;
    automatic_initial requires initial_failure only. Both require explicit workflow and failure-status filters and
    execution limits. Resume count caps the absolute generation; preparation and execution budgets are separate.
    Cancellation remains manual."""
    workflow: str | Unset = UNSET
    created_after: datetime.datetime | Unset = UNSET
    """Inclusive evaluation start; defaults to seven days before the end."""
    created_before: datetime.datetime | Unset = UNSET
    """Inclusive evaluation end; defaults to observation time. Maximum window is 30 days and future bounds are
    rejected."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        expected_version = self.expected_version

        policy = self.policy.to_dict()

        workflow = self.workflow

        created_after: str | Unset = UNSET
        if not isinstance(self.created_after, Unset):
            created_after = self.created_after.isoformat()

        created_before: str | Unset = UNSET
        if not isinstance(self.created_before, Unset):
            created_before = self.created_before.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "expected_version": expected_version,
                "policy": policy,
            }
        )
        if workflow is not UNSET:
            field_dict["workflow"] = workflow
        if created_after is not UNSET:
            field_dict["created_after"] = created_after
        if created_before is not UNSET:
            field_dict["created_before"] = created_before

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_policy import WorkflowRecoveryPolicy

        d = dict(src_dict)
        expected_version = d.pop("expected_version")

        policy = WorkflowRecoveryPolicy.from_dict(d.pop("policy"))

        workflow = d.pop("workflow", UNSET)

        _created_after = d.pop("created_after", UNSET)
        created_after: datetime.datetime | Unset
        if isinstance(_created_after, Unset):
            created_after = UNSET
        else:
            created_after = datetime.datetime.fromisoformat(_created_after)

        _created_before = d.pop("created_before", UNSET)
        created_before: datetime.datetime | Unset
        if isinstance(_created_before, Unset):
            created_before = UNSET
        else:
            created_before = datetime.datetime.fromisoformat(_created_before)

        workflow_recovery_policy_evaluation_request = cls(
            expected_version=expected_version,
            policy=policy,
            workflow=workflow,
            created_after=created_after,
            created_before=created_before,
        )

        workflow_recovery_policy_evaluation_request.additional_properties = d
        return workflow_recovery_policy_evaluation_request

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
