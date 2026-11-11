from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="TracingForward")


@_attrs_define
class TracingForward:
    """Sends a copy of every OpenTelemetry trace and log export the app makes to your own OTLP/HTTP endpoint, from inside
    the app's instance. Requires tracing to be enabled.

    """

    endpoint: str | Unset = UNSET
    """Base OTLP/HTTP URL (http or https, no credentials, query or fragment); /v1/traces and /v1/logs are appended."""
    headers_secret: str | Unset = UNSET
    """Name of an app secret holding request headers as comma-separated key=value pairs, in the
    OTEL_EXPORTER_OTLP_HEADERS format."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        endpoint = self.endpoint

        headers_secret = self.headers_secret

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if endpoint is not UNSET:
            field_dict["endpoint"] = endpoint
        if headers_secret is not UNSET:
            field_dict["headers_secret"] = headers_secret

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        endpoint = d.pop("endpoint", UNSET)

        headers_secret = d.pop("headers_secret", UNSET)

        tracing_forward = cls(
            endpoint=endpoint,
            headers_secret=headers_secret,
        )

        tracing_forward.additional_properties = d
        return tracing_forward

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
