from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_security_request_security_policy import (
    AppSecurityRequestSecurityPolicy,
    check_app_security_request_security_policy,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="AppSecurityRequest")


@_attrs_define
class AppSecurityRequest:
    """PATCH body for `/v1/apps/{slug}/security`. Both fields are optional
    so an operator can change signature enforcement and deploy posture
    independently.

    """

    require_signed: bool | None | Unset = UNSET
    """Operator-only toggle. nil = no change. *true = opt in to signature enforcement (requires the trust list to
    be non-empty). *false = opt out."""
    security_policy: AppSecurityRequestSecurityPolicy | Unset = UNSET
    """Deploy-time response to high-severity configuration findings. off = advisory report only; warn = non-
    blocking; enforce = reject new deploys until remediated and require a trusted signature for OCI images."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        require_signed: bool | None | Unset
        if isinstance(self.require_signed, Unset):
            require_signed = UNSET
        else:
            require_signed = self.require_signed

        security_policy: str | Unset = UNSET
        if not isinstance(self.security_policy, Unset):
            security_policy = self.security_policy

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if require_signed is not UNSET:
            field_dict["require_signed"] = require_signed
        if security_policy is not UNSET:
            field_dict["security_policy"] = security_policy

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)

        def _parse_require_signed(data: object) -> bool | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(bool | None | Unset, data)

        require_signed = _parse_require_signed(d.pop("require_signed", UNSET))

        _security_policy = d.pop("security_policy", UNSET)
        security_policy: AppSecurityRequestSecurityPolicy | Unset
        if isinstance(_security_policy, Unset):
            security_policy = UNSET
        else:
            security_policy = check_app_security_request_security_policy(_security_policy)

        app_security_request = cls(
            require_signed=require_signed,
            security_policy=security_policy,
        )

        app_security_request.additional_properties = d
        return app_security_request

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
