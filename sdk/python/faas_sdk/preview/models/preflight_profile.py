from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="PreflightProfile")


@_attrs_define
class PreflightProfile:
    """The run contract inferred from the source tree."""

    version: str | Unset = UNSET
    framework: str | Unset = UNSET
    framework_version: str | Unset = UNSET
    package_manager: str | Unset = UNSET
    dockerfile_path: str | Unset = UNSET
    start_command: str | Unset = UNSET
    port: int | Unset = UNSET
    health_path: str | Unset = UNSET
    config_file: str | Unset = UNSET
    inferred: bool | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        framework = self.framework

        framework_version = self.framework_version

        package_manager = self.package_manager

        dockerfile_path = self.dockerfile_path

        start_command = self.start_command

        port = self.port

        health_path = self.health_path

        config_file = self.config_file

        inferred = self.inferred

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if version is not UNSET:
            field_dict["version"] = version
        if framework is not UNSET:
            field_dict["framework"] = framework
        if framework_version is not UNSET:
            field_dict["framework_version"] = framework_version
        if package_manager is not UNSET:
            field_dict["package_manager"] = package_manager
        if dockerfile_path is not UNSET:
            field_dict["dockerfile_path"] = dockerfile_path
        if start_command is not UNSET:
            field_dict["start_command"] = start_command
        if port is not UNSET:
            field_dict["port"] = port
        if health_path is not UNSET:
            field_dict["health_path"] = health_path
        if config_file is not UNSET:
            field_dict["config_file"] = config_file
        if inferred is not UNSET:
            field_dict["inferred"] = inferred

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        version = d.pop("version", UNSET)

        framework = d.pop("framework", UNSET)

        framework_version = d.pop("framework_version", UNSET)

        package_manager = d.pop("package_manager", UNSET)

        dockerfile_path = d.pop("dockerfile_path", UNSET)

        start_command = d.pop("start_command", UNSET)

        port = d.pop("port", UNSET)

        health_path = d.pop("health_path", UNSET)

        config_file = d.pop("config_file", UNSET)

        inferred = d.pop("inferred", UNSET)

        preflight_profile = cls(
            version=version,
            framework=framework,
            framework_version=framework_version,
            package_manager=package_manager,
            dockerfile_path=dockerfile_path,
            start_command=start_command,
            port=port,
            health_path=health_path,
            config_file=config_file,
            inferred=inferred,
        )

        preflight_profile.additional_properties = d
        return preflight_profile

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
