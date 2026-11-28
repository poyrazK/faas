from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.decide_release_approval_request_decision import (
    DecideReleaseApprovalRequestDecision,
    check_decide_release_approval_request_decision,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="DecideReleaseApprovalRequest")


@_attrs_define
class DecideReleaseApprovalRequest:
    """One person's decision on a release. A rejection needs a comment."""

    decision: DecideReleaseApprovalRequestDecision
    comment: str | Unset = UNSET
    """Recorded with the decision; required to reject."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        decision: str = self.decision

        comment = self.comment

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "decision": decision,
            }
        )
        if comment is not UNSET:
            field_dict["comment"] = comment

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        decision = check_decide_release_approval_request_decision(d.pop("decision"))

        comment = d.pop("comment", UNSET)

        decide_release_approval_request = cls(
            decision=decision,
            comment=comment,
        )

        decide_release_approval_request.additional_properties = d
        return decide_release_approval_request

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
