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
    override_freeze: str | Unset = UNSET
    """Promote into production while a deploy freeze covers one of the apps; the reason is audited."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from_environment = self.from_environment

        promotion_token = self.promotion_token

        approval_token = self.approval_token

        override_freeze = self.override_freeze

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
        if override_freeze is not UNSET:
            field_dict["override_freeze"] = override_freeze

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        from_environment = d.pop("from_environment")

        promotion_token = d.pop("promotion_token")

        approval_token = d.pop("approval_token", UNSET)

        override_freeze = d.pop("override_freeze", UNSET)

        promote_project_environment_request = cls(
            from_environment=from_environment,
            promotion_token=promotion_token,
            approval_token=approval_token,
            override_freeze=override_freeze,
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
