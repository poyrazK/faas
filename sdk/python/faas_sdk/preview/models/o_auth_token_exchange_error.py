from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.o_auth_token_exchange_error_error import (
    OAuthTokenExchangeErrorError,
    check_o_auth_token_exchange_error_error,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OAuthTokenExchangeError")


@_attrs_define
class OAuthTokenExchangeError:
    """RFC 8693 / OAuth 2.0 token endpoint error response."""

    error: OAuthTokenExchangeErrorError
    error_description: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        error: str = self.error

        error_description = self.error_description

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "error": error,
            }
        )
        if error_description is not UNSET:
            field_dict["error_description"] = error_description

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        error = check_o_auth_token_exchange_error_error(d.pop("error"))

        error_description = d.pop("error_description", UNSET)

        o_auth_token_exchange_error = cls(
            error=error,
            error_description=error_description,
        )

        o_auth_token_exchange_error.additional_properties = d
        return o_auth_token_exchange_error

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
