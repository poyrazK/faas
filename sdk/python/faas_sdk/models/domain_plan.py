from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.domain_plan_version import DomainPlanVersion, check_domain_plan_version
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.domain_binding_change import DomainBindingChange
    from ..models.domain_manifest import DomainManifest


T = TypeVar("T", bound="DomainPlan")


@_attrs_define
class DomainPlan:
    """Deterministic review artifact. The SHA-256 binds the diff to private claim identity, intervening moves, destination
    release and traffic allocations. It contains no ownership proof token.

    """

    version: DomainPlanVersion | Unset = UNSET
    sha256: str | Unset = UNSET
    manifest: DomainManifest | Unset = UNSET
    """Versioned desired bindings. Only listed domains change; omission never removes a binding. At most 20
    bindings in a 128 KiB request. Use a separate file from the application deployment manifest."""
    changes: list[DomainBindingChange] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version: int | Unset = UNSET
        if not isinstance(self.version, Unset):
            version = self.version

        sha256 = self.sha256

        manifest: dict[str, Any] | Unset = UNSET
        if not isinstance(self.manifest, Unset):
            manifest = self.manifest.to_dict()

        changes: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.changes, Unset):
            changes = []
            for changes_item_data in self.changes:
                changes_item = changes_item_data.to_dict()
                changes.append(changes_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if version is not UNSET:
            field_dict["version"] = version
        if sha256 is not UNSET:
            field_dict["sha256"] = sha256
        if manifest is not UNSET:
            field_dict["manifest"] = manifest
        if changes is not UNSET:
            field_dict["changes"] = changes

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.domain_binding_change import DomainBindingChange
        from ..models.domain_manifest import DomainManifest

        d = dict(src_dict)
        _version = d.pop("version", UNSET)
        version: DomainPlanVersion | Unset
        if isinstance(_version, Unset):
            version = UNSET
        else:
            version = check_domain_plan_version(_version)

        sha256 = d.pop("sha256", UNSET)

        _manifest = d.pop("manifest", UNSET)
        manifest: DomainManifest | Unset
        if isinstance(_manifest, Unset):
            manifest = UNSET
        else:
            manifest = DomainManifest.from_dict(_manifest)

        _changes = d.pop("changes", UNSET)
        changes: list[DomainBindingChange] | Unset = UNSET
        if _changes is not UNSET:
            changes = []
            for changes_item_data in _changes:
                changes_item = DomainBindingChange.from_dict(changes_item_data)

                changes.append(changes_item)

        domain_plan = cls(
            version=version,
            sha256=sha256,
            manifest=manifest,
            changes=changes,
        )

        domain_plan.additional_properties = d
        return domain_plan

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
