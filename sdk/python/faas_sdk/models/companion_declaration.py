from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.companion_declaration_disk_io_profile import (
    CompanionDeclarationDiskIoProfile,
    check_companion_declaration_disk_io_profile,
)
from ..models.companion_declaration_preset import CompanionDeclarationPreset, check_companion_declaration_preset
from ..models.companion_declaration_type import CompanionDeclarationType, check_companion_declaration_type
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workload_dependency import WorkloadDependency


T = TypeVar("T", bound="CompanionDeclaration")


@_attrs_define
class CompanionDeclaration:
    """Declared companion configuration only; no commands, environment values or observed health."""

    name: str
    type_: CompanionDeclarationType
    ram_mb: int
    """Declared memory ceiling. Zero inherits the platform default; not observed effective usage."""
    cpu_millicores: int
    """Declared CPU ceiling. Zero inherits the platform default."""
    scratch_mb: int
    """Declared scratch ceiling. Zero derives the runtime default."""
    port: int
    """Declared port. Zero is not an observed listener."""
    primary_ingress: bool
    depends_on: list[WorkloadDependency]
    environment_keys: list[str]
    """Sorted unique names only, including explicit secret-binding environment names. Values and secret references
    are never returned."""
    image: str | Unset = UNSET
    """Declared immutable OCI reference, when returned."""
    preset: CompanionDeclarationPreset | Unset = UNSET
    disk_io_profile: CompanionDeclarationDiskIoProfile | Unset = UNSET
    essential: bool | Unset = UNSET
    """Declared essential policy. Omitted uses the default true; not runtime health."""

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        type_: str = self.type_

        ram_mb = self.ram_mb

        cpu_millicores = self.cpu_millicores

        scratch_mb = self.scratch_mb

        port = self.port

        primary_ingress = self.primary_ingress

        depends_on = []
        for depends_on_item_data in self.depends_on:
            depends_on_item = depends_on_item_data.to_dict()
            depends_on.append(depends_on_item)

        environment_keys = self.environment_keys

        image = self.image

        preset: str | Unset = UNSET
        if not isinstance(self.preset, Unset):
            preset = self.preset

        disk_io_profile: str | Unset = UNSET
        if not isinstance(self.disk_io_profile, Unset):
            disk_io_profile = self.disk_io_profile

        essential = self.essential

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "type": type_,
                "ram_mb": ram_mb,
                "cpu_millicores": cpu_millicores,
                "scratch_mb": scratch_mb,
                "port": port,
                "primary_ingress": primary_ingress,
                "depends_on": depends_on,
                "environment_keys": environment_keys,
            }
        )
        if image is not UNSET:
            field_dict["image"] = image
        if preset is not UNSET:
            field_dict["preset"] = preset
        if disk_io_profile is not UNSET:
            field_dict["disk_io_profile"] = disk_io_profile
        if essential is not UNSET:
            field_dict["essential"] = essential

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workload_dependency import WorkloadDependency

        d = dict(src_dict)
        name = d.pop("name")

        type_ = check_companion_declaration_type(d.pop("type"))

        ram_mb = d.pop("ram_mb")

        cpu_millicores = d.pop("cpu_millicores")

        scratch_mb = d.pop("scratch_mb")

        port = d.pop("port")

        primary_ingress = d.pop("primary_ingress")

        depends_on = []
        _depends_on = d.pop("depends_on")
        for depends_on_item_data in _depends_on:
            depends_on_item = WorkloadDependency.from_dict(depends_on_item_data)

            depends_on.append(depends_on_item)

        environment_keys = cast(list[str], d.pop("environment_keys"))

        image = d.pop("image", UNSET)

        _preset = d.pop("preset", UNSET)
        preset: CompanionDeclarationPreset | Unset
        if isinstance(_preset, Unset):
            preset = UNSET
        else:
            preset = check_companion_declaration_preset(_preset)

        _disk_io_profile = d.pop("disk_io_profile", UNSET)
        disk_io_profile: CompanionDeclarationDiskIoProfile | Unset
        if isinstance(_disk_io_profile, Unset):
            disk_io_profile = UNSET
        else:
            disk_io_profile = check_companion_declaration_disk_io_profile(_disk_io_profile)

        essential = d.pop("essential", UNSET)

        companion_declaration = cls(
            name=name,
            type_=type_,
            ram_mb=ram_mb,
            cpu_millicores=cpu_millicores,
            scratch_mb=scratch_mb,
            port=port,
            primary_ingress=primary_ingress,
            depends_on=depends_on,
            environment_keys=environment_keys,
            image=image,
            preset=preset,
            disk_io_profile=disk_io_profile,
            essential=essential,
        )

        return companion_declaration
