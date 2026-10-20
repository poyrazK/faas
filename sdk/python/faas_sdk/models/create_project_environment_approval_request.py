from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateProjectEnvironmentApprovalRequest")


@_attrs_define
class CreateProjectEnvironmentApprovalRequest:
    """Request to approve one exact plan for a protected environment. Provide exactly one of plan_token or promotion_token."""

    plan_token: str | Unset = UNSET
    """Exact plan token returned by the scan endpoint."""
    promotion_token: str | Unset = UNSET
    """Exact promotion token returned by the environment promotion preview."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        plan_token = self.plan_token

        promotion_token = self.promotion_token

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if plan_token is not UNSET:
            field_dict["plan_token"] = plan_token
        if promotion_token is not UNSET:
            field_dict["promotion_token"] = promotion_token

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        plan_token = d.pop("plan_token", UNSET)

        promotion_token = d.pop("promotion_token", UNSET)

        create_project_environment_approval_request = cls(
            plan_token=plan_token,
            promotion_token=promotion_token,
        )

        create_project_environment_approval_request.additional_properties = d
        return create_project_environment_approval_request

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
