from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="SessionsRevokeAllResponse")


@_attrs_define
class SessionsRevokeAllResponse:
    """Body for `POST /v1/auth/sessions/revoke_all`."""

    revoked: int
    """Number of sibling sessions revoked. The caller's session is NOT included in this count."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        revoked = self.revoked

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "revoked": revoked,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        revoked = d.pop("revoked")

        sessions_revoke_all_response = cls(
            revoked=revoked,
        )

        sessions_revoke_all_response.additional_properties = d
        return sessions_revoke_all_response

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
