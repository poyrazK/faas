from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.import_app_open_api_body_info import ImportAppOpenAPIBodyInfo
    from ..models.import_app_open_api_body_paths import ImportAppOpenAPIBodyPaths


T = TypeVar("T", bound="ImportAppOpenAPIBody")


@_attrs_define
class ImportAppOpenAPIBody:
    """OpenAPI 3.0/3.1 document. The validator only requires openapi, info, paths to be present + object-shaped; everything
    else is passthrough.

    """

    openapi: str
    """OpenAPI version (3.0.x / 3.1.x)."""
    info: ImportAppOpenAPIBodyInfo
    paths: ImportAppOpenAPIBodyPaths
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        openapi = self.openapi

        info = self.info.to_dict()

        paths = self.paths.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "openapi": openapi,
                "info": info,
                "paths": paths,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.import_app_open_api_body_info import ImportAppOpenAPIBodyInfo
        from ..models.import_app_open_api_body_paths import ImportAppOpenAPIBodyPaths

        d = dict(src_dict)
        openapi = d.pop("openapi")

        info = ImportAppOpenAPIBodyInfo.from_dict(d.pop("info"))

        paths = ImportAppOpenAPIBodyPaths.from_dict(d.pop("paths"))

        import_app_open_api_body = cls(
            openapi=openapi,
            info=info,
            paths=paths,
        )

        import_app_open_api_body.additional_properties = d
        return import_app_open_api_body

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
