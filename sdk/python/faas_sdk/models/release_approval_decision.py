from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.release_approval_decision_decision import (
    ReleaseApprovalDecisionDecision,
    check_release_approval_decision_decision,
)
from ..models.release_approval_decision_role import ReleaseApprovalDecisionRole, check_release_approval_decision_role
from ..types import UNSET, Unset

T = TypeVar("T", bound="ReleaseApprovalDecision")


@_attrs_define
class ReleaseApprovalDecision:
    """A decision recorded on a release."""

    approver: str
    role: ReleaseApprovalDecisionRole
    """The approver's organization role when they decided; personal for an app outside an organization."""
    decision: ReleaseApprovalDecisionDecision
    counted: bool
    """Whether the approval counts toward the policy; the deployer's own approval does not unless self-approval is
    allowed."""
    decided_at: datetime.datetime
    comment: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        approver = self.approver

        role: str = self.role

        decision: str = self.decision

        counted = self.counted

        decided_at = self.decided_at.isoformat()

        comment = self.comment

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "approver": approver,
                "role": role,
                "decision": decision,
                "counted": counted,
                "decided_at": decided_at,
            }
        )
        if comment is not UNSET:
            field_dict["comment"] = comment

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        approver = d.pop("approver")

        role = check_release_approval_decision_role(d.pop("role"))

        decision = check_release_approval_decision_decision(d.pop("decision"))

        counted = d.pop("counted")

        decided_at = datetime.datetime.fromisoformat(d.pop("decided_at"))

        comment = d.pop("comment", UNSET)

        release_approval_decision = cls(
            approver=approver,
            role=role,
            decision=decision,
            counted=counted,
            decided_at=decided_at,
            comment=comment,
        )

        release_approval_decision.additional_properties = d
        return release_approval_decision

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
