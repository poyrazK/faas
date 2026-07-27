from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="MFARecoverRequest")


@_attrs_define
class MFARecoverRequest:
    """Body for /recover — one of the 10 recovery codes the
    customer received on /enroll. The hash is removed from
    the stored set; subsequent /recover calls with the same
    code return 401.

    """

    code: str
    csrf_token: str | Unset = UNSET
    """ Dashboard CSRF token for the recover mutation. """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        code = self.code

        csrf_token = self.csrf_token

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "code": code,
            }
        )
        if csrf_token is not UNSET:
            field_dict["csrf_token"] = csrf_token

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = d.pop("code")

        csrf_token = d.pop("csrf_token", UNSET)

        mfa_recover_request = cls(
            code=code,
            csrf_token=csrf_token,
        )

        mfa_recover_request.additional_properties = d
        return mfa_recover_request

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
