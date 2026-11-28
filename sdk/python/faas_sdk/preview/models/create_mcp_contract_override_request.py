from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateMCPContractOverrideRequest")


@_attrs_define
class CreateMCPContractOverrideRequest:
    """Why the next breaking candidate may ship."""

    reason: str
    expires_in_seconds: int | Unset = UNSET
    """Defaults to 3600."""
    deployment_id: UUID | Unset = UNSET
    """Bind the override to one of the app's deployments; without it the app's next breaking production candidate
    uses it, or an unverified one late in its recovery window."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        reason = self.reason

        expires_in_seconds = self.expires_in_seconds

        deployment_id: str | Unset = UNSET
        if not isinstance(self.deployment_id, Unset):
            deployment_id = str(self.deployment_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "reason": reason,
            }
        )
        if expires_in_seconds is not UNSET:
            field_dict["expires_in_seconds"] = expires_in_seconds
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        reason = d.pop("reason")

        expires_in_seconds = d.pop("expires_in_seconds", UNSET)

        _deployment_id = d.pop("deployment_id", UNSET)
        deployment_id: UUID | Unset
        if isinstance(_deployment_id, Unset):
            deployment_id = UNSET
        else:
            deployment_id = UUID(_deployment_id)

        create_mcp_contract_override_request = cls(
            reason=reason,
            expires_in_seconds=expires_in_seconds,
            deployment_id=deployment_id,
        )

        create_mcp_contract_override_request.additional_properties = d
        return create_mcp_contract_override_request

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
