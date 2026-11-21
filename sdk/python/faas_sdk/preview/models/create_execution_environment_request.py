from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.create_execution_environment_request_runtime import (
    CreateExecutionEnvironmentRequestRuntime,
    check_create_execution_environment_request_runtime,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateExecutionEnvironmentRequest")


@_attrs_define
class CreateExecutionEnvironmentRequest:
    """Immutable public language dependencies and exact Wolfi system package pins built inside an isolated builder VM."""

    runtime: CreateExecutionEnvironmentRequestRuntime
    system_packages: list[str] | Unset = UNSET
    """Exact NAME=VERSION-rN Wolfi package pins. Uses only the installer image repositories. May be used without
    language dependencies; direct pins and the installer image enter the recipe identity."""
    requirements: str | Unset = UNSET
    """Exact Python pins and SHA-256 hashes. All transitive dependencies must be pinned. No URLs or index options."""
    package_json: str | Unset = UNSET
    """npm dependency manifest as JSON text. No workspace or registry overrides."""
    package_lock: str | Unset = UNSET
    """npm lockfile v3 as JSON text; public npm registry and SHA-512 integrity only. The combined recipe is at most
    1 MiB."""

    def to_dict(self) -> dict[str, Any]:
        runtime: str = self.runtime

        system_packages: list[str] | Unset = UNSET
        if not isinstance(self.system_packages, Unset):
            system_packages = self.system_packages

        requirements = self.requirements

        package_json = self.package_json

        package_lock = self.package_lock

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "runtime": runtime,
            }
        )
        if system_packages is not UNSET:
            field_dict["system_packages"] = system_packages
        if requirements is not UNSET:
            field_dict["requirements"] = requirements
        if package_json is not UNSET:
            field_dict["package_json"] = package_json
        if package_lock is not UNSET:
            field_dict["package_lock"] = package_lock

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        runtime = check_create_execution_environment_request_runtime(d.pop("runtime"))

        system_packages = cast(list[str], d.pop("system_packages", UNSET))

        requirements = d.pop("requirements", UNSET)

        package_json = d.pop("package_json", UNSET)

        package_lock = d.pop("package_lock", UNSET)

        create_execution_environment_request = cls(
            runtime=runtime,
            system_packages=system_packages,
            requirements=requirements,
            package_json=package_json,
            package_lock=package_lock,
        )

        return create_execution_environment_request
