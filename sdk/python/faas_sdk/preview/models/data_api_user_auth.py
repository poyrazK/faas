from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="DataAPIUserAuth")


@_attrs_define
class DataAPIUserAuth:
    """The issuer, JWKS URL and audience the Data API verifies user tokens against. Live reads verify user tokens with the
    same settings.

    """

    issuer: str
    jwks_url: str
    audience: str

    def to_dict(self) -> dict[str, Any]:
        issuer = self.issuer

        jwks_url = self.jwks_url

        audience = self.audience

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "issuer": issuer,
                "jwks_url": jwks_url,
                "audience": audience,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        issuer = d.pop("issuer")

        jwks_url = d.pop("jwks_url")

        audience = d.pop("audience")

        data_api_user_auth = cls(
            issuer=issuer,
            jwks_url=jwks_url,
            audience=audience,
        )

        return data_api_user_auth
