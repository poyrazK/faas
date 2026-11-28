from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="AppSecretReferenceResponse")


@_attrs_define
class AppSecretReferenceResponse:
    """Stored destination and sealed source names under the catalog environment identity captured for the write."""

    environment_id: UUID
    environment: str
    key: str
    reference: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        environment_id = str(self.environment_id)

        environment = self.environment

        key = self.key

        reference = self.reference

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "environment_id": environment_id,
                "environment": environment,
                "key": key,
                "reference": reference,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        environment_id = UUID(d.pop("environment_id"))

        environment = d.pop("environment")

        key = d.pop("key")

        reference = d.pop("reference")

        app_secret_reference_response = cls(
            environment_id=environment_id,
            environment=environment,
            key=key,
            reference=reference,
        )

        app_secret_reference_response.additional_properties = d
        return app_secret_reference_response

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
