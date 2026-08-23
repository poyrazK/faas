from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.import_app_open_api_response_200_source import (
    ImportAppOpenAPIResponse200Source,
    check_import_app_open_api_response_200_source,
)

T = TypeVar("T", bound="ImportAppOpenAPIResponse200")


@_attrs_define
class ImportAppOpenAPIResponse200:
    app_id: UUID
    source: ImportAppOpenAPIResponse200Source
    openapi_version: str
    endpoint_count: int
    byte_size: int
    captured_at: datetime.datetime
    updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        source: str = self.source

        openapi_version = self.openapi_version

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

        source = check_import_app_open_api_response_200_source(d.pop("source"))

        openapi_version = d.pop("openapi_version")

        endpoint_count = d.pop("endpoint_count")

        byte_size = d.pop("byte_size")

        captured_at = datetime.datetime.fromisoformat(d.pop("captured_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        import_app_open_api_response_200 = cls(
            app_id=app_id,
            source=source,
            openapi_version=openapi_version,
            endpoint_count=endpoint_count,
            byte_size=byte_size,
            captured_at=captured_at,
            updated_at=updated_at,
        )

        import_app_open_api_response_200.additional_properties = d
        return import_app_open_api_response_200

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
