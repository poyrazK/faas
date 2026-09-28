from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.work_policy_response import WorkPolicyResponse


T = TypeVar("T", bound="WorkPolicyListResponse")


@_attrs_define
class WorkPolicyListResponse:
    """All named work policies for one app."""

    policies: list[WorkPolicyResponse]

    def to_dict(self) -> dict[str, Any]:
        policies = []
        for policies_item_data in self.policies:
            policies_item = policies_item_data.to_dict()
            policies.append(policies_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "policies": policies,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.work_policy_response import WorkPolicyResponse

        d = dict(src_dict)
        policies = []
        _policies = d.pop("policies")
        for policies_item_data in _policies:
            policies_item = WorkPolicyResponse.from_dict(policies_item_data)

            policies.append(policies_item)

        work_policy_list_response = cls(
            policies=policies,
        )

        return work_policy_list_response
