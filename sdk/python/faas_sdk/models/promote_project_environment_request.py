from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="PromoteProjectEnvironmentRequest")


@_attrs_define
class PromoteProjectEnvironmentRequest:
    """Request to execute one exact environment promotion."""

    from_environment: str
    promotion_token: str
    """Exact promotion token returned by the preview endpoint."""
    approval_token: str | Unset = UNSET
    """Short-lived approval for a protected target environment."""
    require_bindings: bool | Unset = UNSET
    """Use the dedicated promote-with-bindings route for compatibility with older servers."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from_environment = self.from_environment

        promotion_token = self.promotion_token

        approval_token = self.approval_token

        require_bindings = self.require_bindings

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "from_environment": from_environment,
                "promotion_token": promotion_token,
            }
        )
        if approval_token is not UNSET:
            field_dict["approval_token"] = approval_token
        if require_bindings is not UNSET:
            field_dict["require_bindings"] = require_bindings

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        from_environment = d.pop("from_environment")

        promotion_token = d.pop("promotion_token")

        approval_token = d.pop("approval_token", UNSET)

        require_bindings = d.pop("require_bindings", UNSET)

        promote_project_environment_request = cls(
            from_environment=from_environment,
            promotion_token=promotion_token,
            approval_token=approval_token,
            require_bindings=require_bindings,
        )

        promote_project_environment_request.additional_properties = d
        return promote_project_environment_request

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
