from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="OrgAgentPolicyResponse")


@_attrs_define
class OrgAgentPolicyResponse:
    """An organization's agent policy."""

    agents_allowed: bool
    """Whether agents may act in the organization."""
    allowed_scopes: list[str]
    """The capabilities agents may carry beyond inspection."""
    approval_categories: list[str] | Unset = UNSET
    """Kinds of action every agent acting in the organization must have a person approve."""
    updated_at: datetime.datetime | Unset = UNSET
    """When the policy last changed; absent for an organization that never set one."""
    revoked: int | Unset = UNSET
    """Agent connections a change revoked because the new policy no longer allows them."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        agents_allowed = self.agents_allowed

        allowed_scopes = self.allowed_scopes

        approval_categories: list[str] | Unset = UNSET
        if not isinstance(self.approval_categories, Unset):
            approval_categories = self.approval_categories

        updated_at: str | Unset = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        revoked = self.revoked

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "agents_allowed": agents_allowed,
                "allowed_scopes": allowed_scopes,
            }
        )
        if approval_categories is not UNSET:
            field_dict["approval_categories"] = approval_categories
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at
        if revoked is not UNSET:
            field_dict["revoked"] = revoked

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        agents_allowed = d.pop("agents_allowed")

        allowed_scopes = cast(list[str], d.pop("allowed_scopes"))

        approval_categories = cast(list[str], d.pop("approval_categories", UNSET))

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: datetime.datetime | Unset
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = datetime.datetime.fromisoformat(_updated_at)

        revoked = d.pop("revoked", UNSET)

        org_agent_policy_response = cls(
            agents_allowed=agents_allowed,
            allowed_scopes=allowed_scopes,
            approval_categories=approval_categories,
            updated_at=updated_at,
            revoked=revoked,
        )

        org_agent_policy_response.additional_properties = d
        return org_agent_policy_response

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
