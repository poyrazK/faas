from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.set_release_approval_policy_request_approvers import (
    SetReleaseApprovalPolicyRequestApprovers,
    check_set_release_approval_policy_request_approvers,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="SetReleaseApprovalPolicyRequest")


@_attrs_define
class SetReleaseApprovalPolicyRequest:
    """Sets an app's approval policy. Omitted fields take the defaults: stage 1, a 24-hour timeout, owner or admin
    approvers, no self-approval.

    """

    required_approvals: int
    after_stage: int | Unset = UNSET
    """Default 1."""
    timeout_minutes: int | Unset = UNSET
    """Default 1440."""
    approvers: SetReleaseApprovalPolicyRequestApprovers | Unset = UNSET
    """Default admin."""
    allow_self_approval: bool | Unset = UNSET
    """Default false; always true for an app outside an organization."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        required_approvals = self.required_approvals

        after_stage = self.after_stage

        timeout_minutes = self.timeout_minutes

        approvers: str | Unset = UNSET
        if not isinstance(self.approvers, Unset):
            approvers = self.approvers

        allow_self_approval = self.allow_self_approval

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "required_approvals": required_approvals,
            }
        )
        if after_stage is not UNSET:
            field_dict["after_stage"] = after_stage
        if timeout_minutes is not UNSET:
            field_dict["timeout_minutes"] = timeout_minutes
        if approvers is not UNSET:
            field_dict["approvers"] = approvers
        if allow_self_approval is not UNSET:
            field_dict["allow_self_approval"] = allow_self_approval

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        required_approvals = d.pop("required_approvals")

        after_stage = d.pop("after_stage", UNSET)

        timeout_minutes = d.pop("timeout_minutes", UNSET)

        _approvers = d.pop("approvers", UNSET)
        approvers: SetReleaseApprovalPolicyRequestApprovers | Unset
        if isinstance(_approvers, Unset):
            approvers = UNSET
        else:
            approvers = check_set_release_approval_policy_request_approvers(_approvers)

        allow_self_approval = d.pop("allow_self_approval", UNSET)

        set_release_approval_policy_request = cls(
            required_approvals=required_approvals,
            after_stage=after_stage,
            timeout_minutes=timeout_minutes,
            approvers=approvers,
            allow_self_approval=allow_self_approval,
        )

        set_release_approval_policy_request.additional_properties = d
        return set_release_approval_policy_request

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
