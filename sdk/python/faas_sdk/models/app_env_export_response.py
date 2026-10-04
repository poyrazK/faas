from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.app_env_export_response_values import AppEnvExportResponseValues


T = TypeVar("T", bound="AppEnvExportResponse")


@_attrs_define
class AppEnvExportResponse:
    """Mutable plaintext environment values from one authorized app scope, excluding sealed secrets."""

    app_slug: str
    scope: str
    values: AppEnvExportResponseValues
    """Mutable plaintext env values only. Never includes sealed secrets."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_slug = self.app_slug

        scope = self.scope

        values = self.values.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_slug": app_slug,
                "scope": scope,
                "values": values,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_env_export_response_values import AppEnvExportResponseValues

        d = dict(src_dict)
        app_slug = d.pop("app_slug")

        scope = d.pop("scope")

        values = AppEnvExportResponseValues.from_dict(d.pop("values"))

        app_env_export_response = cls(
            app_slug=app_slug,
            scope=scope,
            values=values,
        )

        app_env_export_response.additional_properties = d
        return app_env_export_response

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
