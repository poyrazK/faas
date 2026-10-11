from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="DurableEntityInvokeResponse")


@_attrs_define
class DurableEntityInvokeResponse:
    """The acknowledged entity result. Replay returns the original value and version."""

    value: Any
    """JSON result from the committed transition."""
    version: int
    """Committed transition version, or original version on replay."""
    replayed: bool
    """True when an existing receipt supplied the result."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        value = self.value

        version = self.version

        replayed = self.replayed

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "value": value,
                "version": version,
                "replayed": replayed,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        value = d.pop("value")

        version = d.pop("version")

        replayed = d.pop("replayed")

        durable_entity_invoke_response = cls(
            value=value,
            version=version,
            replayed=replayed,
        )

        durable_entity_invoke_response.additional_properties = d
        return durable_entity_invoke_response

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
