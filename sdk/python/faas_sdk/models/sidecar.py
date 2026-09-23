from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.sidecar_cpu_millicores import SidecarCpuMillicores, check_sidecar_cpu_millicores
from ..models.sidecar_disk_io_profile import SidecarDiskIoProfile, check_sidecar_disk_io_profile
from ..models.sidecar_preset import SidecarPreset, check_sidecar_preset
from ..models.sidecar_type import SidecarType, check_sidecar_type
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_manifest_healthcheck import AppManifestHealthcheck
    from ..models.sidecar_env import SidecarEnv
    from ..models.workload_dependency import WorkloadDependency


T = TypeVar("T", bound="Sidecar")


@_attrs_define
class Sidecar:
    """One entry in the deploy request's preferred `companions` array
    (legacy name: `sidecars`). Up to 2 helpers per app (1 init
    + 1 sidecar; the array is type-uniqueness + 2-capped at
    the schema layer via migration 00095's CHECK constraint).
    Stateless only — stateful base images (Postgres, Redis,
    MySQL, MongoDB, etc.) are rejected at the API gate
    with 403 `sidecar_stateful_denied` and again at imaged
    (PR-B). Image references must be digest-pinned
    (`repo@sha256:...`); tag references are rejected with
    400 `sidecar_invalid_image`. Env values are
    envelope-sealed at rest via secretbox (namespace
    `"sidecar_env"`); the wire shape is plaintext, the
    column is sealed ciphertext.

    - `name` matches RFC 1123 label (lowercase alphanumeric
      + dash, 1..63 chars, starts with [a-z0-9]). Unique
      within a single request.
    - `preset` selects a platform-managed helper. A preset may omit
      `image`; apid resolves an operator-pinned immutable digest.
    - `image` is required for a custom helper and must be a
      digest-pinned OCI reference. Tag references are rejected.
    - `type` ∈ {`init`, `sidecar`}. At most one of each per
      deployment.
    - `cmd` is the argv (image's ENTRYPOINT unchanged; CMD
      overridden). Every element non-empty.
    - `env` is plaintext on the wire, sealed at rest. Keys
      per `^[A-Z][A-Z0-9_]*$`; per-value byte cap = plan
      `EnvValueMaxBytes`. Plaintext values NEVER appear in
      any log, audit, or error.
    - `port` ∈ {0, 1..65535}. 0 = absent.
    - `primary_ingress` routes the application's normal hostname and
      custom domains through this long-running helper. It requires port.
    - `ram_mb` ∈ {0, 32..512}. 0 = inherit plan RAM.
    - `scratch_mb` ∈ {0, 16..512}. 0 = platform default; explicit values cap the sidecar's writable `/tmp` tmpfs.
    - `cpu_millicores` ∈ {0, 250, 500, 1000}. 0 = inherit app CPU quota.
    - `disk_io_profile` ∈ {`low`, `standard`, `high`}. Omit to inherit the guest default; profiles map to per-workload
    cgroup I/O weights.
    - `essential` defaults to true. If true and the workload
      exits non-zero, the dependency set fails
      (`failure_class=user_error`) and essential long-running
      sidecars restart-loop. If false, the failure is logged
      and the other workloads continue.
    - `startup_probe` optionally replaces the image's baked OCI
      `HEALTHCHECK` for this workload. It uses the exec-style
      `AppManifestHealthcheck` shape; set `test` to [`NONE`] to
      explicitly disable the image probe.
    - `depends_on` optionally gates this workload on `main` or
      another sidecar. Conditions are `started`, `healthy`, and
      `completed_successfully`; omitted condition means `started`.
      Init workloads are implicit prerequisites of main and long-running
      sidecars. Cycles and unknown workload names are rejected.

    """

    name: str
    """RFC 1123 label (lowercase alphanumeric + dash, 1..63 chars, starts with [a-z0-9])."""
    type_: SidecarType
    """`init` runs once before the main workload (DB migrator shape). `sidecar` runs alongside (metrics scraper
    shape)."""
    image: str | Unset = UNSET
    """Digest-pinned OCI reference (repo@sha256:...). Tag references rejected with 400 `sidecar_invalid_image`."""
    preset: SidecarPreset | Unset = UNSET
    """Platform-managed companion preset. The installation must configure an immutable image digest."""
    cmd: list[str] | Unset = UNSET
    """Argv. Image's ENTRYPOINT unchanged; CMD overridden. Every element non-empty."""
    env: SidecarEnv | Unset = UNSET
    """Plaintext env map (sealed at rest). Keys `^[A-Z][A-Z0-9_]*$`; per-value byte cap = plan EnvValueMaxBytes."""
    port: int | Unset = UNSET
    """Listen port. 0 = absent / fall back to image default."""
    primary_ingress: bool | Unset = False
    """Route the app's primary public hostname through this long-running companion. Requires an explicit port."""
    ram_mb: int | Unset = UNSET
    """Cgroup memory ceiling for this sidecar. 0 = inherit plan RAM; 32..512 enforced at the API."""
    scratch_mb: int | Unset = UNSET
    """Writable /tmp tmpfs ceiling for this sidecar in MB. 0 = platform default; explicit values must be 16..512."""
    cpu_millicores: SidecarCpuMillicores | Unset = 0
    """Sustained cgroup CPU allowance in millicores. 0 = inherit app CPU quota."""
    disk_io_profile: SidecarDiskIoProfile | Unset = UNSET
    """Per-workload guest cgroup I/O scheduling policy. Omit to inherit the guest default."""
    essential: bool | Unset = UNSET
    """Defaults to true. Essential workload failure fails the set; non-essential failure is logged and contained."""
    startup_probe: AppManifestHealthcheck | Unset = UNSET
    """AppManifest-level projection of the OCI HEALTHCHECK shape (ADR-136 §Decision 3-4). Durations are integer
    seconds at the JSON boundary to match OCI/Docker conventions. Runtime polling lands in M-2 (ADR-X5); M-1
    surfaces the field for the registry-pull path."""
    depends_on: list[WorkloadDependency] | Unset = UNSET
    """Optional workload lifecycle dependencies. Init workloads are implicit prerequisites of main and long-running
    sidecars."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        type_: str = self.type_

        image = self.image

        preset: str | Unset = UNSET
        if not isinstance(self.preset, Unset):
            preset = self.preset

        cmd: list[str] | Unset = UNSET
        if not isinstance(self.cmd, Unset):
            cmd = self.cmd

        env: dict[str, Any] | Unset = UNSET
        if not isinstance(self.env, Unset):
            env = self.env.to_dict()

        port = self.port

        primary_ingress = self.primary_ingress

        ram_mb = self.ram_mb

        scratch_mb = self.scratch_mb

        cpu_millicores: int | Unset = UNSET
        if not isinstance(self.cpu_millicores, Unset):
            cpu_millicores = self.cpu_millicores

        disk_io_profile: str | Unset = UNSET
        if not isinstance(self.disk_io_profile, Unset):
            disk_io_profile = self.disk_io_profile

        essential = self.essential

        startup_probe: dict[str, Any] | Unset = UNSET
        if not isinstance(self.startup_probe, Unset):
            startup_probe = self.startup_probe.to_dict()

        depends_on: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.depends_on, Unset):
            depends_on = []
            for depends_on_item_data in self.depends_on:
                depends_on_item = depends_on_item_data.to_dict()
                depends_on.append(depends_on_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "type": type_,
            }
        )
        if image is not UNSET:
            field_dict["image"] = image
        if preset is not UNSET:
            field_dict["preset"] = preset
        if cmd is not UNSET:
            field_dict["cmd"] = cmd
        if env is not UNSET:
            field_dict["env"] = env
        if port is not UNSET:
            field_dict["port"] = port
        if primary_ingress is not UNSET:
            field_dict["primary_ingress"] = primary_ingress
        if ram_mb is not UNSET:
            field_dict["ram_mb"] = ram_mb
        if scratch_mb is not UNSET:
            field_dict["scratch_mb"] = scratch_mb
        if cpu_millicores is not UNSET:
            field_dict["cpu_millicores"] = cpu_millicores
        if disk_io_profile is not UNSET:
            field_dict["disk_io_profile"] = disk_io_profile
        if essential is not UNSET:
            field_dict["essential"] = essential
        if startup_probe is not UNSET:
            field_dict["startup_probe"] = startup_probe
        if depends_on is not UNSET:
            field_dict["depends_on"] = depends_on

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_manifest_healthcheck import AppManifestHealthcheck
        from ..models.sidecar_env import SidecarEnv
        from ..models.workload_dependency import WorkloadDependency

        d = dict(src_dict)
        name = d.pop("name")

        type_ = check_sidecar_type(d.pop("type"))

        image = d.pop("image", UNSET)

        _preset = d.pop("preset", UNSET)
        preset: SidecarPreset | Unset
        if isinstance(_preset, Unset):
            preset = UNSET
        else:
            preset = check_sidecar_preset(_preset)

        cmd = cast(list[str], d.pop("cmd", UNSET))

        _env = d.pop("env", UNSET)
        env: SidecarEnv | Unset
        if isinstance(_env, Unset):
            env = UNSET
        else:
            env = SidecarEnv.from_dict(_env)

        port = d.pop("port", UNSET)

        primary_ingress = d.pop("primary_ingress", UNSET)

        ram_mb = d.pop("ram_mb", UNSET)

        scratch_mb = d.pop("scratch_mb", UNSET)

        _cpu_millicores = d.pop("cpu_millicores", UNSET)
        cpu_millicores: SidecarCpuMillicores | Unset
        if isinstance(_cpu_millicores, Unset):
            cpu_millicores = UNSET
        else:
            cpu_millicores = check_sidecar_cpu_millicores(_cpu_millicores)

        _disk_io_profile = d.pop("disk_io_profile", UNSET)
        disk_io_profile: SidecarDiskIoProfile | Unset
        if isinstance(_disk_io_profile, Unset):
            disk_io_profile = UNSET
        else:
            disk_io_profile = check_sidecar_disk_io_profile(_disk_io_profile)

        essential = d.pop("essential", UNSET)

        _startup_probe = d.pop("startup_probe", UNSET)
        startup_probe: AppManifestHealthcheck | Unset
        if isinstance(_startup_probe, Unset):
            startup_probe = UNSET
        else:
            startup_probe = AppManifestHealthcheck.from_dict(_startup_probe)

        _depends_on = d.pop("depends_on", UNSET)
        depends_on: list[WorkloadDependency] | Unset = UNSET
        if _depends_on is not UNSET:
            depends_on = []
            for depends_on_item_data in _depends_on:
                depends_on_item = WorkloadDependency.from_dict(depends_on_item_data)

                depends_on.append(depends_on_item)

        sidecar = cls(
            name=name,
            type_=type_,
            image=image,
            preset=preset,
            cmd=cmd,
            env=env,
            port=port,
            primary_ingress=primary_ingress,
            ram_mb=ram_mb,
            scratch_mb=scratch_mb,
            cpu_millicores=cpu_millicores,
            disk_io_profile=disk_io_profile,
            essential=essential,
            startup_probe=startup_probe,
            depends_on=depends_on,
        )

        sidecar.additional_properties = d
        return sidecar

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
