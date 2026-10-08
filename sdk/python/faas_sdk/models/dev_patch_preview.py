from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.dev_patch_preview_reason import DevPatchPreviewReason, check_dev_patch_preview_reason
from ..types import UNSET, Unset

T = TypeVar("T", bound="DevPatchPreview")


@_attrs_define
class DevPatchPreview:
    """Set only on the response to a developer source upload (`gregale dev`). Reports whether the sync could have been
    applied as a live source patch to the deployment that was live at upload time (ADR-740 phase 1). It is a measurement
    only; the normal developer build always runs.

    """

    eligible: bool
    changed_paths: int
    """Files added"""
    patch_bytes: int
    """Total size of the added or modified files."""
    reason: DevPatchPreviewReason | Unset = UNSET
    """Why the sync could not use a live patch. Absent when eligible."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        eligible = self.eligible

        changed_paths = self.changed_paths

        patch_bytes = self.patch_bytes

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "eligible": eligible,
                "changed_paths": changed_paths,
                "patch_bytes": patch_bytes,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        eligible = d.pop("eligible")

        changed_paths = d.pop("changed_paths")

        patch_bytes = d.pop("patch_bytes")

        _reason = d.pop("reason", UNSET)
        reason: DevPatchPreviewReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_dev_patch_preview_reason(_reason)

        dev_patch_preview = cls(
            eligible=eligible,
            changed_paths=changed_paths,
            patch_bytes=patch_bytes,
            reason=reason,
        )

        dev_patch_preview.additional_properties = d
        return dev_patch_preview

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
