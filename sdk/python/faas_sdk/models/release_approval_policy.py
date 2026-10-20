from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.release_approval_policy_approvers import (
    ReleaseApprovalPolicyApprovers,
    check_release_approval_policy_approvers,
)

T = TypeVar("T", bound="ReleaseApprovalPolicy")


@_attrs_define
class ReleaseApprovalPolicy:
    """An app's release approval policy (ADR-20261020085706)."""

    app: str
    required_approvals: int
    """Distinct eligible people who must approve each production release."""
    after_stage: int
    """The canary stage a release waits in, counted from 1."""
    timeout_minutes: int
    """How long a release may wait before its rollout is aborted."""
    approvers: ReleaseApprovalPolicyApprovers
    """admin: organization owners and admins; developer: developers too."""
    allow_self_approval: bool
    """Whether the deployer's own approval counts."""
    updated_by: str
    created_at: datetime.datetime
    updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app = self.app

        required_approvals = self.required_approvals

        after_stage = self.after_stage

        timeout_minutes = self.timeout_minutes

        approvers: str = self.approvers

        allow_self_approval = self.allow_self_approval

        updated_by = self.updated_by

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app": app,
                "required_approvals": required_approvals,
                "after_stage": after_stage,
                "timeout_minutes": timeout_minutes,
                "approvers": approvers,
                "allow_self_approval": allow_self_approval,
                "updated_by": updated_by,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app = d.pop("app")

        required_approvals = d.pop("required_approvals")

        after_stage = d.pop("after_stage")

        timeout_minutes = d.pop("timeout_minutes")

        approvers = check_release_approval_policy_approvers(d.pop("approvers"))

        allow_self_approval = d.pop("allow_self_approval")

        updated_by = d.pop("updated_by")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        release_approval_policy = cls(
            app=app,
            required_approvals=required_approvals,
            after_stage=after_stage,
            timeout_minutes=timeout_minutes,
            approvers=approvers,
            allow_self_approval=allow_self_approval,
            updated_by=updated_by,
            created_at=created_at,
            updated_at=updated_at,
        )

        release_approval_policy.additional_properties = d
        return release_approval_policy

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
