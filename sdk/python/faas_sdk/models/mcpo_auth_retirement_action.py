from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcpo_auth_retirement_action_action import (
    MCPOAuthRetirementActionAction,
    check_mcpo_auth_retirement_action_action,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="MCPOAuthRetirementAction")


@_attrs_define
class MCPOAuthRetirementAction:
    """Explicit retirement recovery; allowing active grant revocation may invalidate a replacement connection."""

    action: MCPOAuthRetirementActionAction
    allow_active_grant_revocation: bool | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        action: str = self.action

        allow_active_grant_revocation = self.allow_active_grant_revocation

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "action": action,
            }
        )
        if allow_active_grant_revocation is not UNSET:
            field_dict["allow_active_grant_revocation"] = allow_active_grant_revocation

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        action = check_mcpo_auth_retirement_action_action(d.pop("action"))

        allow_active_grant_revocation = d.pop("allow_active_grant_revocation", UNSET)

        mcpo_auth_retirement_action = cls(
            action=action,
            allow_active_grant_revocation=allow_active_grant_revocation,
        )

        mcpo_auth_retirement_action.additional_properties = d
        return mcpo_auth_retirement_action

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
