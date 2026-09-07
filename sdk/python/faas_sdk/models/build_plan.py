from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.build_plan_class_type_1 import BuildPlanClassType1, check_build_plan_class_type_1
from ..models.build_plan_class_type_2_type_1 import BuildPlanClassType2Type1, check_build_plan_class_type_2_type_1
from ..models.build_plan_class_type_3_type_1 import BuildPlanClassType3Type1, check_build_plan_class_type_3_type_1
from ..models.build_plan_framework import BuildPlanFramework, check_build_plan_framework
from ..types import UNSET, Unset

T = TypeVar("T", bound="BuildPlan")


@_attrs_define
class BuildPlan:
    """Effective build plan surfaced on DeploymentResponse (issue #961 / zero-config profile PR). Captured from the exact
    source archive at enqueue time and retained after spool cleanup; legacy rows fall back to marker detection when the
    spool is still available. Embedded on DeploymentResponse; never returned by a dedicated route.

    """

    framework: BuildPlanFramework
    """Compatibility framework family derived from the persisted source profile. `unknown` means no supported
    framework was inferred (monorepo / custom build)."""
    runtime: None | str | Unset = UNSET
    """Runtime the app is pinned to (eg `node22`, `python312`). Echoed from app.Runtime. nil for apps without a
    runtime set (image deploys)."""
    version: None | str | Unset = UNSET
    """Framework version extracted from the detected marker (eg `package.json` `engines.node`, `requirements.txt`
    head pin). nil when the marker has no version or framework is `unknown`."""
    entrypoint: None | str | Unset = UNSET
    """Effective start command from the persisted source profile, replaced by an explicit entrypoint override when
    supplied."""
    port: int | None | Unset = UNSET
    """Effective listen port from the persisted source profile, replaced by an explicit port override when
    supplied."""
    health_path: None | str | Unset = UNSET
    """Effective readiness path selected by the source profile or deployment override."""
    class_: BuildPlanClassType1 | BuildPlanClassType2Type1 | BuildPlanClassType3Type1 | None | Unset = UNSET
    """App class from `app.Type` — `app` for plain apps, `function` for function rewrites (spec §4.2)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        framework: str = self.framework

        runtime: None | str | Unset
        if isinstance(self.runtime, Unset):
            runtime = UNSET
        else:
            runtime = self.runtime

        version: None | str | Unset
        if isinstance(self.version, Unset):
            version = UNSET
        else:
            version = self.version

        entrypoint: None | str | Unset
        if isinstance(self.entrypoint, Unset):
            entrypoint = UNSET
        else:
            entrypoint = self.entrypoint

        port: int | None | Unset
        if isinstance(self.port, Unset):
            port = UNSET
        else:
            port = self.port

        health_path: None | str | Unset
        if isinstance(self.health_path, Unset):
            health_path = UNSET
        else:
            health_path = self.health_path

        class_: None | str | Unset
        if isinstance(self.class_, Unset):
            class_ = UNSET
        elif isinstance(self.class_, str):
            class_ = self.class_
        elif isinstance(self.class_, str):
            class_ = self.class_
        elif isinstance(self.class_, str):
            class_ = self.class_
        else:
            class_ = self.class_

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "framework": framework,
            }
        )
        if runtime is not UNSET:
            field_dict["runtime"] = runtime
        if version is not UNSET:
            field_dict["version"] = version
        if entrypoint is not UNSET:
            field_dict["entrypoint"] = entrypoint
        if port is not UNSET:
            field_dict["port"] = port
        if health_path is not UNSET:
            field_dict["health_path"] = health_path
        if class_ is not UNSET:
            field_dict["class"] = class_

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        framework = check_build_plan_framework(d.pop("framework"))

        def _parse_runtime(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        runtime = _parse_runtime(d.pop("runtime", UNSET))

        def _parse_version(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        version = _parse_version(d.pop("version", UNSET))

        def _parse_entrypoint(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        entrypoint = _parse_entrypoint(d.pop("entrypoint", UNSET))

        def _parse_port(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        port = _parse_port(d.pop("port", UNSET))

        def _parse_health_path(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        health_path = _parse_health_path(d.pop("health_path", UNSET))

        def _parse_class_(
            data: object,
        ) -> BuildPlanClassType1 | BuildPlanClassType2Type1 | BuildPlanClassType3Type1 | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                class_type_1 = check_build_plan_class_type_1(data)

                return class_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, str):
                    raise TypeError()
                class_type_2_type_1 = check_build_plan_class_type_2_type_1(data)

                return class_type_2_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, str):
                    raise TypeError()
                class_type_3_type_1 = check_build_plan_class_type_3_type_1(data)

                return class_type_3_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(BuildPlanClassType1 | BuildPlanClassType2Type1 | BuildPlanClassType3Type1 | None | Unset, data)

        class_ = _parse_class_(d.pop("class", UNSET))

        build_plan = cls(
            framework=framework,
            runtime=runtime,
            version=version,
            entrypoint=entrypoint,
            port=port,
            health_path=health_path,
            class_=class_,
        )

        build_plan.additional_properties = d
        return build_plan

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
