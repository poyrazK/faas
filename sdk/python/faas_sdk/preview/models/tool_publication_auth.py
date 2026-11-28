from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

T = TypeVar("T", bound="ToolPublicationAuth")


@_attrs_define
class ToolPublicationAuth:
    """Reviewed external OAuth issuer, signing keys, resource and scopes for one private publication."""

    issuer: str
    jwks_url: str
    resource: str
    scopes: list[str]

    def to_dict(self) -> dict[str, Any]:
        issuer = self.issuer

        jwks_url = self.jwks_url

        resource = self.resource

        scopes = self.scopes

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "issuer": issuer,
                "jwks_url": jwks_url,
                "resource": resource,
                "scopes": scopes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        issuer = d.pop("issuer")

        jwks_url = d.pop("jwks_url")

        resource = d.pop("resource")

        scopes = cast(list[str], d.pop("scopes"))

        tool_publication_auth = cls(
            issuer=issuer,
            jwks_url=jwks_url,
            resource=resource,
            scopes=scopes,
        )

        return tool_publication_auth
