from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.preview_production_changes_response_configuration_changed_groups_item import (
    PreviewProductionChangesResponseConfigurationChangedGroupsItem,
    check_preview_production_changes_response_configuration_changed_groups_item,
)

if TYPE_CHECKING:
    from ..models.preview_artifact_response import PreviewArtifactResponse


T = TypeVar("T", bound="PreviewProductionChangesResponse")


@_attrs_define
class PreviewProductionChangesResponse:
    """Non-secret preview differences from the production parent."""

    artifact_changed: bool
    preview_artifact: PreviewArtifactResponse
    """Strongest available non-secret deployment artifact identity."""
    production_artifact: PreviewArtifactResponse
    """Strongest available non-secret deployment artifact identity."""
    configuration_changed_groups: list[PreviewProductionChangesResponseConfigurationChangedGroupsItem]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        artifact_changed = self.artifact_changed

        preview_artifact = self.preview_artifact.to_dict()

        production_artifact = self.production_artifact.to_dict()

        configuration_changed_groups = []
        for configuration_changed_groups_item_data in self.configuration_changed_groups:
            configuration_changed_groups_item: str = configuration_changed_groups_item_data
            configuration_changed_groups.append(configuration_changed_groups_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "artifact_changed": artifact_changed,
                "preview_artifact": preview_artifact,
                "production_artifact": production_artifact,
                "configuration_changed_groups": configuration_changed_groups,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.preview_artifact_response import PreviewArtifactResponse

        d = dict(src_dict)
        artifact_changed = d.pop("artifact_changed")

        preview_artifact = PreviewArtifactResponse.from_dict(d.pop("preview_artifact"))

        production_artifact = PreviewArtifactResponse.from_dict(d.pop("production_artifact"))

        configuration_changed_groups = []
        _configuration_changed_groups = d.pop("configuration_changed_groups")
        for configuration_changed_groups_item_data in _configuration_changed_groups:
            configuration_changed_groups_item = (
                check_preview_production_changes_response_configuration_changed_groups_item(
                    configuration_changed_groups_item_data
                )
            )

            configuration_changed_groups.append(configuration_changed_groups_item)

        preview_production_changes_response = cls(
            artifact_changed=artifact_changed,
            preview_artifact=preview_artifact,
            production_artifact=production_artifact,
            configuration_changed_groups=configuration_changed_groups,
        )

        preview_production_changes_response.additional_properties = d
        return preview_production_changes_response

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
