from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_manifest_execution_mode_type_1 import (
    AppManifestExecutionModeType1,
    check_app_manifest_execution_mode_type_1,
)
from ..models.app_manifest_execution_mode_type_2_type_1 import (
    AppManifestExecutionModeType2Type1,
    check_app_manifest_execution_mode_type_2_type_1,
)
from ..models.app_manifest_execution_mode_type_3_type_1 import (
    AppManifestExecutionModeType3Type1,
    check_app_manifest_execution_mode_type_3_type_1,
)
from ..models.app_manifest_restart_policy_type_1 import (
    AppManifestRestartPolicyType1,
    check_app_manifest_restart_policy_type_1,
)
from ..models.app_manifest_restart_policy_type_2_type_1 import (
    AppManifestRestartPolicyType2Type1,
    check_app_manifest_restart_policy_type_2_type_1,
)
from ..models.app_manifest_restart_policy_type_3_type_1 import (
    AppManifestRestartPolicyType3Type1,
    check_app_manifest_restart_policy_type_3_type_1,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_manifest_env import AppManifestEnv
    from ..models.app_manifest_env_secrets import AppManifestEnvSecrets
    from ..models.app_manifest_healthcheck import AppManifestHealthcheck
    from ..models.service_replicas import ServiceReplicas


T = TypeVar("T", bound="AppManifest")


@_attrs_define
class AppManifest:
    """App manifest: environment variables, build commands, working directory, healthcheck, user, and Dockerfile-as-source
    flag (§ux 6.3). The optional `env_secrets` field carries sealed-secret refs ("secret:NAME" strings) resolved by the
    host at wake time against the app_secrets table (issue #460 / ADR-053 §Decision 1). Values are NEVER sealed
    ciphertext — only refs. M-1 (ADR-136) widens the contract additively with `healthcheck`, `stop_signal`,
    `stop_grace_period` from the OCI image-config spec; old guest-init ignores unknown fields per JSON semantics, so the
    widen is wire-compatible. M-2 (ADR-137 + ADR-138) widens additively with `execution_mode`, `restart_policy`,
    `startup_deadline_s`, `max_retries`, and `service_replicas` — these govern the lifecycle contract (request vs
    service vs worker vs job) and the per-mode replica scaffold. Defaults preserve today's behaviour
    (execution_mode=request, restart_policy=on-failure).

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
    """OCI STOPSIGNAL (default SIGTERM). Wired into the Engine.StopInstance signal-and-grace flow in M-2."""
    stop_grace_period: None | str | Unset = UNSET
    """OCI StopGracePeriod as a Go duration string (e.g. "30s"). Per-plan cap (Hobby 30s, Pro 60s, Scale 120s)
    enforced by Validate() — ADR-138 §Decision 4."""
    execution_mode: (
        AppManifestExecutionModeType1
        | AppManifestExecutionModeType2Type1
        | AppManifestExecutionModeType3Type1
        | None
        | Unset
    ) = UNSET
    """Lifecycle contract for this app (ADR-137 §Decision 1). Default 'request' preserves today's behaviour."""
    restart_policy: (
        AppManifestRestartPolicyType1
        | AppManifestRestartPolicyType2Type1
        | AppManifestRestartPolicyType3Type1
        | None
        | Unset
    ) = UNSET
    """Restart behaviour when the main workload exits (ADR-137 §Decision 2). Default is mode-derived: always for
    worker/service, no for job, on-failure for request."""
    startup_deadline_s: int | None | Unset = UNSET
    """Upper bound on time-to-ready (seconds). Per-plan cap enforced by Validate() (ADR-138 §Decision 3). Default 0
    means 'use plan default'."""
    max_retries: int | None | Unset = UNSET
    """Consecutive restart-attempt cap (ADR-138 §Decision 3). Per-plan cap: Hobby 5, Pro 10, Scale 20. Default 0
    means 'use plan default'."""
    service_replicas: ServiceReplicas | Unset = UNSET
    """Per-deployment replica scaffold for execution_mode='service' (ADR-137 §Decision 3, M-2 + M-4 workstream E).
    Replica count is bounded by ServiceReplicasMax per plan (Hobby 3, Pro 5, Scale 20). min ≤ desired ≤ max must
    hold. Foundation here; rolling-deploy / rollback / image-digest pinning semantics land in M-4."""
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

        execution_mode: None | str | Unset
        if isinstance(self.execution_mode, Unset):
            execution_mode = UNSET
        elif isinstance(self.execution_mode, str):
            execution_mode = self.execution_mode
        elif isinstance(self.execution_mode, str):
            execution_mode = self.execution_mode
        elif isinstance(self.execution_mode, str):
            execution_mode = self.execution_mode
        else:
            execution_mode = self.execution_mode

        restart_policy: None | str | Unset
        if isinstance(self.restart_policy, Unset):
            restart_policy = UNSET
        elif isinstance(self.restart_policy, str):
            restart_policy = self.restart_policy
        elif isinstance(self.restart_policy, str):
            restart_policy = self.restart_policy
        elif isinstance(self.restart_policy, str):
            restart_policy = self.restart_policy
        else:
            restart_policy = self.restart_policy

        startup_deadline_s: int | None | Unset
        if isinstance(self.startup_deadline_s, Unset):
            startup_deadline_s = UNSET
        else:
            startup_deadline_s = self.startup_deadline_s

        max_retries: int | None | Unset
        if isinstance(self.max_retries, Unset):
            max_retries = UNSET
        else:
            max_retries = self.max_retries

        service_replicas: dict[str, Any] | Unset = UNSET
        if not isinstance(self.service_replicas, Unset):
            service_replicas = self.service_replicas.to_dict()

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
        if execution_mode is not UNSET:
            field_dict["execution_mode"] = execution_mode
        if restart_policy is not UNSET:
            field_dict["restart_policy"] = restart_policy
        if startup_deadline_s is not UNSET:
            field_dict["startup_deadline_s"] = startup_deadline_s
        if max_retries is not UNSET:
            field_dict["max_retries"] = max_retries
        if service_replicas is not UNSET:
            field_dict["service_replicas"] = service_replicas

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_manifest_env import AppManifestEnv
        from ..models.app_manifest_env_secrets import AppManifestEnvSecrets
        from ..models.app_manifest_healthcheck import AppManifestHealthcheck
        from ..models.service_replicas import ServiceReplicas

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

        def _parse_execution_mode(
            data: object,
        ) -> (
            AppManifestExecutionModeType1
            | AppManifestExecutionModeType2Type1
            | AppManifestExecutionModeType3Type1
            | None
            | Unset
        ):
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                execution_mode_type_1 = check_app_manifest_execution_mode_type_1(data)

                return execution_mode_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, str):
                    raise TypeError()
                execution_mode_type_2_type_1 = check_app_manifest_execution_mode_type_2_type_1(data)

                return execution_mode_type_2_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, str):
                    raise TypeError()
                execution_mode_type_3_type_1 = check_app_manifest_execution_mode_type_3_type_1(data)

                return execution_mode_type_3_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(
                AppManifestExecutionModeType1
                | AppManifestExecutionModeType2Type1
                | AppManifestExecutionModeType3Type1
                | None
                | Unset,
                data,
            )

        execution_mode = _parse_execution_mode(d.pop("execution_mode", UNSET))

        def _parse_restart_policy(
            data: object,
        ) -> (
            AppManifestRestartPolicyType1
            | AppManifestRestartPolicyType2Type1
            | AppManifestRestartPolicyType3Type1
            | None
            | Unset
        ):
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                restart_policy_type_1 = check_app_manifest_restart_policy_type_1(data)

                return restart_policy_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, str):
                    raise TypeError()
                restart_policy_type_2_type_1 = check_app_manifest_restart_policy_type_2_type_1(data)

                return restart_policy_type_2_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, str):
                    raise TypeError()
                restart_policy_type_3_type_1 = check_app_manifest_restart_policy_type_3_type_1(data)

                return restart_policy_type_3_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(
                AppManifestRestartPolicyType1
                | AppManifestRestartPolicyType2Type1
                | AppManifestRestartPolicyType3Type1
                | None
                | Unset,
                data,
            )

        restart_policy = _parse_restart_policy(d.pop("restart_policy", UNSET))

        def _parse_startup_deadline_s(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        startup_deadline_s = _parse_startup_deadline_s(d.pop("startup_deadline_s", UNSET))

        def _parse_max_retries(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        max_retries = _parse_max_retries(d.pop("max_retries", UNSET))

        _service_replicas = d.pop("service_replicas", UNSET)
        service_replicas: ServiceReplicas | Unset
        if isinstance(_service_replicas, Unset):
            service_replicas = UNSET
        else:
            service_replicas = ServiceReplicas.from_dict(_service_replicas)

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
            execution_mode=execution_mode,
            restart_policy=restart_policy,
            startup_deadline_s=startup_deadline_s,
            max_retries=max_retries,
            service_replicas=service_replicas,
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
