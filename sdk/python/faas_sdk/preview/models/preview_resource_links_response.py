from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="PreviewResourceLinksResponse")


@_attrs_define
class PreviewResourceLinksResponse:
    """Public preview URL and native APIs for its logs, metrics, and effective app configuration."""

    url: str
    logs: str
    metrics: str
    configuration: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        url = self.url

        logs = self.logs

        metrics = self.metrics

        configuration = self.configuration

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "url": url,
                "logs": logs,
                "metrics": metrics,
                "configuration": configuration,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        url = d.pop("url")

        logs = d.pop("logs")

        metrics = d.pop("metrics")

        configuration = d.pop("configuration")

        preview_resource_links_response = cls(
            url=url,
            logs=logs,
            metrics=metrics,
            configuration=configuration,
        )

        preview_resource_links_response.additional_properties = d
        return preview_resource_links_response

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
