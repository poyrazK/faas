from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_open_api_import_response_openapi_version import (
    AppOpenAPIImportResponseOpenapiVersion,
    check_app_open_api_import_response_openapi_version,
)
from ..models.app_open_api_import_response_source import (
    AppOpenAPIImportResponseSource,
    check_app_open_api_import_response_source,
)

T = TypeVar("T", bound="AppOpenAPIImportResponse")


@_attrs_define
class AppOpenAPIImportResponse:
    """Response body for `POST /v1/apps/{slug}/openapi` (issue #975
    item #2 / ADR-126). One row per app in `app_openapi_docs`,
    last-write-wins. Source is always `manual_import` — cold-
    boot captures go to `deployment_openapi_docs` (item #1).
    `endpoint_count` is the number of HTTP operations in the
    imported doc's `paths.*`; `byte_size` is the raw body size
    the handler enforced against
    `state.OpenAPIImportMaxDocBytes` (256 KiB).

    """

    app_id: UUID
    """App UUID the import row is bound to."""
    source: AppOpenAPIImportResponseSource
    """Row source. Always `manual_import` for this endpoint."""
    openapi_version: AppOpenAPIImportResponseOpenapiVersion
    """OpenAPI spec version the imported doc declares."""
    endpoint_count: int
    """Number of HTTP operations in the imported doc."""
    byte_size: int
    """Raw body size in bytes (state.OpenAPIImportMaxDocBytes = 256 KiB)."""
    captured_at: datetime.datetime
    """First-import timestamp; preserved across re-imports."""
    updated_at: datetime.datetime
    """Most-recent write timestamp; bumped on every import."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        source: str = self.source

        openapi_version: str = self.openapi_version

        endpoint_count = self.endpoint_count

        byte_size = self.byte_size

        captured_at = self.captured_at.isoformat()

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "source": source,
                "openapi_version": openapi_version,
                "endpoint_count": endpoint_count,
                "byte_size": byte_size,
                "captured_at": captured_at,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        source = check_app_open_api_import_response_source(d.pop("source"))

        openapi_version = check_app_open_api_import_response_openapi_version(d.pop("openapi_version"))

        endpoint_count = d.pop("endpoint_count")

        byte_size = d.pop("byte_size")

        captured_at = datetime.datetime.fromisoformat(d.pop("captured_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        app_open_api_import_response = cls(
            app_id=app_id,
            source=source,
            openapi_version=openapi_version,
            endpoint_count=endpoint_count,
            byte_size=byte_size,
            captured_at=captured_at,
            updated_at=updated_at,
        )

        app_open_api_import_response.additional_properties = d
        return app_open_api_import_response

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
