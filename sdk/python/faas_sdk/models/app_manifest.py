from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_manifest_env import AppManifestEnv


T = TypeVar("T", bound="AppManifest")


@_attrs_define
class AppManifest:
    """App manifest: environment variables, build commands, working directory, healthcheck, user, and Dockerfile-as-source
    flag (§ux 6.3).

    """

    entrypoint: list[str]
    env: AppManifestEnv | Unset = UNSET
    working_dir: None | str | Unset = UNSET
    port: int | None | Unset = UNSET
    healthz: None | str | Unset = UNSET
    user: None | str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        entrypoint = self.entrypoint

        env: dict[str, Any] | Unset = UNSET
        if not isinstance(self.env, Unset):
            env = self.env.to_dict()

        working_dir: None | str | Unset
        if isinstance(self.working_dir, Unset):
            working_dir = UNSET
        else:
            working_dir = self.working_dir

        port: int | None | Unset
        if isinstance(self.port, Unset):
            port = UNSET
        else:
            port = self.port

        healthz: None | str | Unset
        if isinstance(self.healthz, Unset):
            healthz = UNSET
        else:
            healthz = self.healthz

        user: None | str | Unset
        if isinstance(self.user, Unset):
            user = UNSET
        else:
            user = self.user

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "entrypoint": entrypoint,
            }
        )
        if env is not UNSET:
            field_dict["env"] = env
        if working_dir is not UNSET:
            field_dict["working_dir"] = working_dir
        if port is not UNSET:
            field_dict["port"] = port
        if healthz is not UNSET:
            field_dict["healthz"] = healthz
        if user is not UNSET:
            field_dict["user"] = user

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_manifest_env import AppManifestEnv

        d = dict(src_dict)
        entrypoint = cast(list[str], d.pop("entrypoint"))

        _env = d.pop("env", UNSET)
        env: AppManifestEnv | Unset
        if isinstance(_env, Unset):
            env = UNSET
        else:
            env = AppManifestEnv.from_dict(_env)

        def _parse_working_dir(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        working_dir = _parse_working_dir(d.pop("working_dir", UNSET))

        def _parse_port(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        port = _parse_port(d.pop("port", UNSET))

        def _parse_healthz(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        healthz = _parse_healthz(d.pop("healthz", UNSET))

        def _parse_user(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        user = _parse_user(d.pop("user", UNSET))

        app_manifest = cls(
            entrypoint=entrypoint,
            env=env,
            working_dir=working_dir,
            port=port,
            healthz=healthz,
            user=user,
        )

        app_manifest.additional_properties = d
        return app_manifest

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
