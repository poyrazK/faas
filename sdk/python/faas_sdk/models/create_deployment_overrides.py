from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.create_deployment_overrides_env import CreateDeploymentOverridesEnv
    from ..models.create_deployment_overrides_env_secrets import CreateDeploymentOverridesEnvSecrets
    from ..models.deployment_healthcheck import DeploymentHealthcheck
    from ..models.deployment_liveness_probe import DeploymentLivenessProbe


T = TypeVar("T", bound="CreateDeploymentOverrides")


@_attrs_define
class CreateDeploymentOverrides:
    """Fargate-shaped deploy-time override object on `POST /v1/apps/{slug}/deployments`
    (issue #460 / ADR-053). Field list is FROZEN — six fields, no more. Any extra
    field on this object 400s the request. ADR-053 §Decision 1 documents the freeze;
    the handler enforces it via `DisallowUnknownFields` on the JSON decoder.

    - `entrypoint` replaces the OCI image's ENTRYPOINT/CMD argv at exec time.
    - `cmd` is appended to `entrypoint` (mirrors the OCI runtime contract).
    - `env` is plaintext; env-var keys must match `^[A-Z][A-Z0-9_]*$`; per-value
      byte cap = plan `EnvValueMaxBytes`.
    - `env_secrets` carries `secret:NAME` REFS — the runtime resolver (PR-B)
      strips the prefix and looks up `NAME` against the existing `app_secrets`
      table. The ref name must match `^[A-Z][A-Z0-9_]*$`.
    - `env` + `env_secrets` share the plan `EnvVarsMax` quota — no bypass by
      mixing the two surfaces.
    - `port` is per-deployment (1..65535; 0 = absent / fall back to image default).
    - `healthcheck` is the readiness-probe shape; the actual HTTP probe ships
      in a follow-up ADR.

    """

    entrypoint: list[str] | Unset = UNSET
    """Replaces the OCI image's ENTRYPOINT/CMD argv. Each element must be non-empty."""
    cmd: list[str] | Unset = UNSET
    """Appended to entrypoint at exec time (OCI runtime contract). Each element must be non-empty."""
    env: CreateDeploymentOverridesEnv | Unset = UNSET
    """Plaintext env map applied at boot. Keys: `^[A-Z][A-Z0-9_]*$`. Per-value byte cap = plan EnvValueMaxBytes."""
    env_secrets: CreateDeploymentOverridesEnvSecrets | Unset = UNSET
    """Sealed-secret-ref env map. Each VALUE is `secret:NAME`; the runtime resolver looks up `NAME` against
    `app_secrets` at wake. Counts against the shared `EnvVarsMax` cap with `env`."""
    port: int | Unset = UNSET
    """Listen port; 0 = absent / fall back to image default (today 8080)."""
    healthcheck: DeploymentHealthcheck | None | Unset = UNSET
    """Readiness-probe shape. Persisted today; the HTTP probe variant ships in a follow-up ADR."""
    liveness_probe: DeploymentLivenessProbe | None | Unset = UNSET
    """Liveness-probe override (issue #554 / ADR-078). The host (cmd/vmmd)
    polls the guest's vsock 1028 STREAM on every `interval_s`; after
    `consecutive_failures` consecutive non-2xx (or timeout / conn-refused)
    responses, the VM is destroyed and schedd cold-boots it from rootfs
    (per ADR-005 — never snapshot-restore). 3 restarts in 300 s
    park the deployment.

    Per-plan gates (issue #554): Free is locked out (Plan.LivenessAllowed()
    returns false; the apid handler rejects with
    `plan_liveness_probe_not_allowed` BEFORE the DB is touched); Hobby,
    Pro, Scale inherit the 5 s / 3 consecutive / 60 s cooldown / 3 in 300 s
    defaults. Pro and Scale may select standard gRPC health.v1 Check
    on the runtime port; Free and Hobby remain HTTP-only for liveness.
    """
    scope: None | str | Unset = UNSET
    """Override-object per-deployment env scope (ADR-091 / PR-D). Lowercase alnum + dash, 3..40 chars, no
    leading/trailing dash. nil/omitted = inherit top-level scope or `default`."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.deployment_healthcheck import DeploymentHealthcheck
        from ..models.deployment_liveness_probe import DeploymentLivenessProbe

        entrypoint: list[str] | Unset = UNSET
        if not isinstance(self.entrypoint, Unset):
            entrypoint = self.entrypoint

        cmd: list[str] | Unset = UNSET
        if not isinstance(self.cmd, Unset):
            cmd = self.cmd

        env: dict[str, Any] | Unset = UNSET
        if not isinstance(self.env, Unset):
            env = self.env.to_dict()

        env_secrets: dict[str, Any] | Unset = UNSET
        if not isinstance(self.env_secrets, Unset):
            env_secrets = self.env_secrets.to_dict()

        port = self.port

        healthcheck: dict[str, Any] | None | Unset
        if isinstance(self.healthcheck, Unset):
            healthcheck = UNSET
        elif isinstance(self.healthcheck, DeploymentHealthcheck):
            healthcheck = self.healthcheck.to_dict()
        else:
            healthcheck = self.healthcheck

        liveness_probe: dict[str, Any] | None | Unset
        if isinstance(self.liveness_probe, Unset):
            liveness_probe = UNSET
        elif isinstance(self.liveness_probe, DeploymentLivenessProbe):
            liveness_probe = self.liveness_probe.to_dict()
        else:
            liveness_probe = self.liveness_probe

        scope: None | str | Unset
        if isinstance(self.scope, Unset):
            scope = UNSET
        else:
            scope = self.scope

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if entrypoint is not UNSET:
            field_dict["entrypoint"] = entrypoint
        if cmd is not UNSET:
            field_dict["cmd"] = cmd
        if env is not UNSET:
            field_dict["env"] = env
        if env_secrets is not UNSET:
            field_dict["env_secrets"] = env_secrets
        if port is not UNSET:
            field_dict["port"] = port
        if healthcheck is not UNSET:
            field_dict["healthcheck"] = healthcheck
        if liveness_probe is not UNSET:
            field_dict["liveness_probe"] = liveness_probe
        if scope is not UNSET:
            field_dict["scope"] = scope

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.create_deployment_overrides_env import CreateDeploymentOverridesEnv
        from ..models.create_deployment_overrides_env_secrets import CreateDeploymentOverridesEnvSecrets
        from ..models.deployment_healthcheck import DeploymentHealthcheck
        from ..models.deployment_liveness_probe import DeploymentLivenessProbe

        d = dict(src_dict)
        entrypoint = cast(list[str], d.pop("entrypoint", UNSET))

        cmd = cast(list[str], d.pop("cmd", UNSET))

        _env = d.pop("env", UNSET)
        env: CreateDeploymentOverridesEnv | Unset
        if isinstance(_env, Unset):
            env = UNSET
        else:
            env = CreateDeploymentOverridesEnv.from_dict(_env)

        _env_secrets = d.pop("env_secrets", UNSET)
        env_secrets: CreateDeploymentOverridesEnvSecrets | Unset
        if isinstance(_env_secrets, Unset):
            env_secrets = UNSET
        else:
            env_secrets = CreateDeploymentOverridesEnvSecrets.from_dict(_env_secrets)

        port = d.pop("port", UNSET)

        def _parse_healthcheck(data: object) -> DeploymentHealthcheck | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                healthcheck_type_0 = DeploymentHealthcheck.from_dict(data)

                return healthcheck_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(DeploymentHealthcheck | None | Unset, data)

        healthcheck = _parse_healthcheck(d.pop("healthcheck", UNSET))

        def _parse_liveness_probe(data: object) -> DeploymentLivenessProbe | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                liveness_probe_type_0 = DeploymentLivenessProbe.from_dict(data)

                return liveness_probe_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(DeploymentLivenessProbe | None | Unset, data)

        liveness_probe = _parse_liveness_probe(d.pop("liveness_probe", UNSET))

        def _parse_scope(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        scope = _parse_scope(d.pop("scope", UNSET))

        create_deployment_overrides = cls(
            entrypoint=entrypoint,
            cmd=cmd,
            env=env,
            env_secrets=env_secrets,
            port=port,
            healthcheck=healthcheck,
            liveness_probe=liveness_probe,
            scope=scope,
        )

        create_deployment_overrides.additional_properties = d
        return create_deployment_overrides

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
