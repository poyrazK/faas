from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="ToolPublicationVerificationRequest")


@_attrs_define
class ToolPublicationVerificationRequest:
    """Exact native candidate and catalog pin with the reviewed transient behavioral fixture bytes."""

    deployment_id: UUID
    catalog_sha256: str
    fixtures_json: str
    """Exact reviewed fixture bytes; SHA-256 must match the publication intent"""

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        catalog_sha256 = self.catalog_sha256

        fixtures_json = self.fixtures_json

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "deployment_id": deployment_id,
                "catalog_sha256": catalog_sha256,
                "fixtures_json": fixtures_json,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        catalog_sha256 = d.pop("catalog_sha256")

        fixtures_json = d.pop("fixtures_json")

        tool_publication_verification_request = cls(
            deployment_id=deployment_id,
            catalog_sha256=catalog_sha256,
            fixtures_json=fixtures_json,
        )

        return tool_publication_verification_request
