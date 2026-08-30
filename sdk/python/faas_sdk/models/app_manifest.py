from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_manifest_env import AppManifestEnv
    from ..models.app_manifest_env_secrets import AppManifestEnvSecrets
    from ..models.app_manifest_healthcheck import AppManifestHealthcheck


T = TypeVar("T", bound="AppManifest")


@_attrs_define
class AppManifest:
    """App manifest: environment variables, build commands, working directory, healthcheck, user, and Dockerfile-as-source
    flag (§ux 6.3). The optional `env_secrets` field carries sealed-secret refs ("secret:NAME" strings) resolved by the
    host at wake time against the app_secrets table (issue #460 / ADR-053 §Decision 1). Values are NEVER sealed
    ciphertext — only refs. M-1 (ADR-136) widens the contract additively with `healthcheck`, `stop_signal`,
    `stop_grace_period` from the OCI image-config spec; old guest-init ignores unknown fields per JSON semantics, so the
    widen is wire-compatible.

    """

    entrypoint: list[str]
    env: AppManifestEnv | Unset = UNSET
    env_secrets: AppManifestEnvSecrets | Unset = UNSET
    """Env override via sealed-secret refs. Each value is "secret:NAME"; the host resolver looks up NAME against
    the app_secrets table at wake."""
    working_dir: None | str | Unset = UNSET
    port: int | None | Unset = UNSET
    healthz: None | str | Unset = UNSET
    user: None | str | Unset = UNSET
    healthcheck: AppManifestHealthcheck | Unset = UNSET
    """AppManifest-level projection of the OCI HEALTHCHECK shape (ADR-136 §Decision 3-4). Durations are integer
    seconds at the JSON boundary to match OCI/Docker conventions. Runtime polling lands in M-2 (ADR-X5); M-1
    surfaces the field for the registry-pull path."""
    stop_signal: None | str | Unset = UNSET
    """OCI STOPSIGNAL (default SIGTERM). Runtime wiring lands in M-2."""
    stop_grace_period: None | str | Unset = UNSET
    """OCI StopGracePeriod as a Go duration string (e.g. "5m"). Capped at MaxAppManifestStopGracePeriod (5m).
    Currently always zero — populated by M-2."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        entrypoint = self.entrypoint

        env: dict[str, Any] | Unset = UNSET
        if not isinstance(self.env, Unset):
            env = self.env.to_dict()

        env_secrets: dict[str, Any] | Unset = UNSET
        if not isinstance(self.env_secrets, Unset):
            env_secrets = self.env_secrets.to_dict()

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

        healthcheck: dict[str, Any] | Unset = UNSET
        if not isinstance(self.healthcheck, Unset):
            healthcheck = self.healthcheck.to_dict()

        stop_signal: None | str | Unset
        if isinstance(self.stop_signal, Unset):
            stop_signal = UNSET
        else:
            stop_signal = self.stop_signal

        stop_grace_period: None | str | Unset
        if isinstance(self.stop_grace_period, Unset):
            stop_grace_period = UNSET
        else:
            stop_grace_period = self.stop_grace_period

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "entrypoint": entrypoint,
            }
        )
        if env is not UNSET:
            field_dict["env"] = env
        if env_secrets is not UNSET:
            field_dict["env_secrets"] = env_secrets
        if working_dir is not UNSET:
            field_dict["working_dir"] = working_dir
        if port is not UNSET:
            field_dict["port"] = port
        if healthz is not UNSET:
            field_dict["healthz"] = healthz
        if user is not UNSET:
            field_dict["user"] = user
        if healthcheck is not UNSET:
            field_dict["healthcheck"] = healthcheck
        if stop_signal is not UNSET:
            field_dict["stop_signal"] = stop_signal
        if stop_grace_period is not UNSET:
            field_dict["stop_grace_period"] = stop_grace_period

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_manifest_env import AppManifestEnv
        from ..models.app_manifest_env_secrets import AppManifestEnvSecrets
        from ..models.app_manifest_healthcheck import AppManifestHealthcheck

        d = dict(src_dict)
        entrypoint = cast(list[str], d.pop("entrypoint"))

        _env = d.pop("env", UNSET)
        env: AppManifestEnv | Unset
        if isinstance(_env, Unset):
            env = UNSET
        else:
            env = AppManifestEnv.from_dict(_env)

        _env_secrets = d.pop("env_secrets", UNSET)
        env_secrets: AppManifestEnvSecrets | Unset
        if isinstance(_env_secrets, Unset):
            env_secrets = UNSET
        else:
            env_secrets = AppManifestEnvSecrets.from_dict(_env_secrets)

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

        _healthcheck = d.pop("healthcheck", UNSET)
        healthcheck: AppManifestHealthcheck | Unset
        if isinstance(_healthcheck, Unset):
            healthcheck = UNSET
        else:
            healthcheck = AppManifestHealthcheck.from_dict(_healthcheck)

        def _parse_stop_signal(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        stop_signal = _parse_stop_signal(d.pop("stop_signal", UNSET))

        def _parse_stop_grace_period(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        stop_grace_period = _parse_stop_grace_period(d.pop("stop_grace_period", UNSET))

        app_manifest = cls(
            entrypoint=entrypoint,
            env=env,
            env_secrets=env_secrets,
            working_dir=working_dir,
            port=port,
            healthz=healthz,
            user=user,
            healthcheck=healthcheck,
            stop_signal=stop_signal,
            stop_grace_period=stop_grace_period,
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
