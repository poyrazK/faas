from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_security_response_security_policy import (
    AppSecurityResponseSecurityPolicy,
    check_app_security_response_security_policy,
)

T = TypeVar("T", bound="AppSecurityResponse")


@_attrs_define
class AppSecurityResponse:
    """PATCH /v1/apps/{slug}/security response body — the updated security controls."""

    require_signed: bool
    """The current state of the require_signed flag after the patch."""
    security_policy: AppSecurityResponseSecurityPolicy
    """The current deploy-time posture policy after the patch."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        require_signed = self.require_signed

        security_policy: str = self.security_policy

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "require_signed": require_signed,
                "security_policy": security_policy,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        require_signed = d.pop("require_signed")

        security_policy = check_app_security_response_security_policy(d.pop("security_policy"))

        app_security_response = cls(
            require_signed=require_signed,
            security_policy=security_policy,
        )

        app_security_response.additional_properties = d
        return app_security_response

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
