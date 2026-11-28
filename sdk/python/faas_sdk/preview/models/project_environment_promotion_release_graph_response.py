from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectEnvironmentPromotionReleaseGraphResponse")


@_attrs_define
class ProjectEnvironmentPromotionReleaseGraphResponse:
    """Immutable graph identities involved in a graph-aware promotion and rollback."""

    ttl_seconds: int
    source_release_set_id: UUID | Unset = UNSET
    previous_target_release_set_id: UUID | Unset = UNSET
    target_release_set_id: UUID | Unset = UNSET
    restored_target_release_set_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        ttl_seconds = self.ttl_seconds

        source_release_set_id: str | Unset = UNSET
        if not isinstance(self.source_release_set_id, Unset):
            source_release_set_id = str(self.source_release_set_id)

        previous_target_release_set_id: str | Unset = UNSET
        if not isinstance(self.previous_target_release_set_id, Unset):
            previous_target_release_set_id = str(self.previous_target_release_set_id)

        target_release_set_id: str | Unset = UNSET
        if not isinstance(self.target_release_set_id, Unset):
            target_release_set_id = str(self.target_release_set_id)

        restored_target_release_set_id: str | Unset = UNSET
        if not isinstance(self.restored_target_release_set_id, Unset):
            restored_target_release_set_id = str(self.restored_target_release_set_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "ttl_seconds": ttl_seconds,
            }
        )
        if source_release_set_id is not UNSET:
            field_dict["source_release_set_id"] = source_release_set_id
        if previous_target_release_set_id is not UNSET:
            field_dict["previous_target_release_set_id"] = previous_target_release_set_id
        if target_release_set_id is not UNSET:
            field_dict["target_release_set_id"] = target_release_set_id
        if restored_target_release_set_id is not UNSET:
            field_dict["restored_target_release_set_id"] = restored_target_release_set_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        ttl_seconds = d.pop("ttl_seconds")

        _source_release_set_id = d.pop("source_release_set_id", UNSET)
        source_release_set_id: UUID | Unset
        if isinstance(_source_release_set_id, Unset):
            source_release_set_id = UNSET
        else:
            source_release_set_id = UUID(_source_release_set_id)

        _previous_target_release_set_id = d.pop("previous_target_release_set_id", UNSET)
        previous_target_release_set_id: UUID | Unset
        if isinstance(_previous_target_release_set_id, Unset):
            previous_target_release_set_id = UNSET
        else:
            previous_target_release_set_id = UUID(_previous_target_release_set_id)

        _target_release_set_id = d.pop("target_release_set_id", UNSET)
        target_release_set_id: UUID | Unset
        if isinstance(_target_release_set_id, Unset):
            target_release_set_id = UNSET
        else:
            target_release_set_id = UUID(_target_release_set_id)

        _restored_target_release_set_id = d.pop("restored_target_release_set_id", UNSET)
        restored_target_release_set_id: UUID | Unset
        if isinstance(_restored_target_release_set_id, Unset):
            restored_target_release_set_id = UNSET
        else:
            restored_target_release_set_id = UUID(_restored_target_release_set_id)

        project_environment_promotion_release_graph_response = cls(
            ttl_seconds=ttl_seconds,
            source_release_set_id=source_release_set_id,
            previous_target_release_set_id=previous_target_release_set_id,
            target_release_set_id=target_release_set_id,
            restored_target_release_set_id=restored_target_release_set_id,
        )

        project_environment_promotion_release_graph_response.additional_properties = d
        return project_environment_promotion_release_graph_response

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
