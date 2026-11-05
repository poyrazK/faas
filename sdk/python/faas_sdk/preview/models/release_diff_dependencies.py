from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.release_diff_package import ReleaseDiffPackage


T = TypeVar("T", bound="ReleaseDiffDependencies")


@_attrs_define
class ReleaseDiffDependencies:
    """How the builds' SBOM packages differ. Each list holds at most 100 entries."""

    known: bool
    """False when either release has no build SBOM."""
    added: list[ReleaseDiffPackage]
    removed: list[ReleaseDiffPackage]
    changed: list[ReleaseDiffPackage]
    note: str | Unset = UNSET
    truncated: bool | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        known = self.known

        added = []
        for added_item_data in self.added:
            added_item = added_item_data.to_dict()
            added.append(added_item)

        removed = []
        for removed_item_data in self.removed:
            removed_item = removed_item_data.to_dict()
            removed.append(removed_item)

        changed = []
        for changed_item_data in self.changed:
            changed_item = changed_item_data.to_dict()
            changed.append(changed_item)

        note = self.note

        truncated = self.truncated

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "known": known,
                "added": added,
                "removed": removed,
                "changed": changed,
            }
        )
        if note is not UNSET:
            field_dict["note"] = note
        if truncated is not UNSET:
            field_dict["truncated"] = truncated

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.release_diff_package import ReleaseDiffPackage

        d = dict(src_dict)
        known = d.pop("known")

        added = []
        _added = d.pop("added")
        for added_item_data in _added:
            added_item = ReleaseDiffPackage.from_dict(added_item_data)

            added.append(added_item)

        removed = []
        _removed = d.pop("removed")
        for removed_item_data in _removed:
            removed_item = ReleaseDiffPackage.from_dict(removed_item_data)

            removed.append(removed_item)

        changed = []
        _changed = d.pop("changed")
        for changed_item_data in _changed:
            changed_item = ReleaseDiffPackage.from_dict(changed_item_data)

            changed.append(changed_item)

        note = d.pop("note", UNSET)

        truncated = d.pop("truncated", UNSET)

        release_diff_dependencies = cls(
            known=known,
            added=added,
            removed=removed,
            changed=changed,
            note=note,
            truncated=truncated,
        )

        release_diff_dependencies.additional_properties = d
        return release_diff_dependencies

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
