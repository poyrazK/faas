from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RotateAppSecretResponse")


@_attrs_define
class RotateAppSecretResponse:
    """Response from `POST /v1/apps/{slug}/secrets/{key}/rotate`. The
    `kid` is the age-1... recipient string of the host identity that
    sealed the new envelope (ADR-089 D4); `rotated_at` is
    RFC3339Nano so two rotates in the same second produce distinct
    timestamps. Empty `kid` means the row was rotated but the kid
    was not stampable (rare — happens only if apid started without
    host.age.pub, which the handler 503s for instead).

    """

    key: str
    rotated_at: datetime.datetime
    kid: str | Unset = UNSET
    """age-1... recipient string of the host identity that sealed this row. Empty if kid was not stampable."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        rotated_at = self.rotated_at.isoformat()

        kid = self.kid

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "key": key,
                "rotated_at": rotated_at,
            }
        )
        if kid is not UNSET:
            field_dict["kid"] = kid

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        key = d.pop("key")

        rotated_at = datetime.datetime.fromisoformat(d.pop("rotated_at"))

        kid = d.pop("kid", UNSET)

        rotate_app_secret_response = cls(
            key=key,
            rotated_at=rotated_at,
            kid=kid,
        )

        rotate_app_secret_response.additional_properties = d
        return rotate_app_secret_response

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
