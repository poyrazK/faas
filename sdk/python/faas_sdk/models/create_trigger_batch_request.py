from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="CreateTriggerBatchRequest")


@_attrs_define
class CreateTriggerBatchRequest:
    """Inline-manifest path (POST /v1/triggers:batch_create) — fire a
    gregale.yaml blob at the server without staging a tarball.
    The handler re-uses validateManifestBytes from the manifest
    apply path.

    """

    app_id: str
    manifest_yaml: str
    """Raw gregale.yaml triggers: list."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        manifest_yaml = self.manifest_yaml

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "manifest_yaml": manifest_yaml,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = d.pop("app_id")

        manifest_yaml = d.pop("manifest_yaml")

        create_trigger_batch_request = cls(
            app_id=app_id,
            manifest_yaml=manifest_yaml,
        )

        create_trigger_batch_request.additional_properties = d
        return create_trigger_batch_request

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
