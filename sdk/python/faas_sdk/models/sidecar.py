from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.sidecar_type import SidecarType, check_sidecar_type
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.sidecar_env import SidecarEnv
    from ..models.workload_dependency import WorkloadDependency


T = TypeVar("T", bound="Sidecar")


@_attrs_define
class Sidecar:
    """One entry in the deploy request's `sidecars` array
    (issue #463 / ADR-068). Up to 2 sidecars per app (1 init
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
    - `image` is the digest-pinned OCI reference. Tag
      references rejected. State images rejected.
    - `type` ∈ {`init`, `sidecar`}. At most one of each per
      deployment.
    - `cmd` is the argv (image's ENTRYPOINT unchanged; CMD
      overridden). Every element non-empty.
    - `env` is plaintext on the wire, sealed at rest. Keys
      per `^[A-Z][A-Z0-9_]*$`; per-value byte cap = plan
      `EnvValueMaxBytes`. Plaintext values NEVER appear in
      any log, audit, or error.
    - `port` ∈ {0, 1..65535}. 0 = absent.
    - `ram_mb` ∈ {0, 32..512}. 0 = inherit plan RAM.
    - `essential` defaults to true. If true and the workload
      exits non-zero, the dependency set fails
      (`failure_class=user_error`) and essential long-running
      sidecars restart-loop. If false, the failure is logged
      and the other workloads continue.
    - `depends_on` optionally gates this workload on `main` or
      another sidecar. Conditions are `started`, `healthy`, and
      `completed_successfully`; omitted condition means `started`.
      Init workloads are implicit prerequisites of main and long-running
      sidecars. Cycles and unknown workload names are rejected.

    """

    name: str
    """RFC 1123 label (lowercase alphanumeric + dash, 1..63 chars, starts with [a-z0-9])."""
    image: str
    """Digest-pinned OCI reference (repo@sha256:...). Tag references rejected with 400 `sidecar_invalid_image`."""
    type_: SidecarType
    """`init` runs once before the main workload (DB migrator shape). `sidecar` runs alongside (metrics scraper
    shape)."""
    cmd: list[str] | Unset = UNSET
    """Argv. Image's ENTRYPOINT unchanged; CMD overridden. Every element non-empty."""
    env: SidecarEnv | Unset = UNSET
    """Plaintext env map (sealed at rest). Keys `^[A-Z][A-Z0-9_]*$`; per-value byte cap = plan EnvValueMaxBytes."""
    port: int | Unset = UNSET
    """Listen port. 0 = absent / fall back to image default."""
    ram_mb: int | Unset = UNSET
    """Cgroup memory ceiling for this sidecar. 0 = inherit plan RAM; 32..512 enforced at the API."""
    essential: bool | Unset = UNSET
    """Defaults to true. Essential workload failure fails the set; non-essential failure is logged and contained."""
    depends_on: list[WorkloadDependency] | Unset = UNSET
    """Optional workload lifecycle dependencies. Init workloads are implicit prerequisites of main and long-running
    sidecars."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        image = self.image

        type_: str = self.type_

        cmd: list[str] | Unset = UNSET
        if not isinstance(self.cmd, Unset):
            cmd = self.cmd

        env: dict[str, Any] | Unset = UNSET
        if not isinstance(self.env, Unset):
            env = self.env.to_dict()

        port = self.port

        ram_mb = self.ram_mb

        essential = self.essential

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
                "image": image,
                "type": type_,
            }
        )
        if cmd is not UNSET:
            field_dict["cmd"] = cmd
        if env is not UNSET:
            field_dict["env"] = env
        if port is not UNSET:
            field_dict["port"] = port
        if ram_mb is not UNSET:
            field_dict["ram_mb"] = ram_mb
        if essential is not UNSET:
            field_dict["essential"] = essential
        if depends_on is not UNSET:
            field_dict["depends_on"] = depends_on

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.sidecar_env import SidecarEnv
        from ..models.workload_dependency import WorkloadDependency

        d = dict(src_dict)
        name = d.pop("name")

        image = d.pop("image")

        type_ = check_sidecar_type(d.pop("type"))

        cmd = cast(list[str], d.pop("cmd", UNSET))

        _env = d.pop("env", UNSET)
        env: SidecarEnv | Unset
        if isinstance(_env, Unset):
            env = UNSET
        else:
            env = SidecarEnv.from_dict(_env)

        port = d.pop("port", UNSET)

        ram_mb = d.pop("ram_mb", UNSET)

        essential = d.pop("essential", UNSET)

        _depends_on = d.pop("depends_on", UNSET)
        depends_on: list[WorkloadDependency] | Unset = UNSET
        if _depends_on is not UNSET:
            depends_on = []
            for depends_on_item_data in _depends_on:
                depends_on_item = WorkloadDependency.from_dict(depends_on_item_data)

                depends_on.append(depends_on_item)

        sidecar = cls(
            name=name,
            image=image,
            type_=type_,
            cmd=cmd,
            env=env,
            port=port,
            ram_mb=ram_mb,
            essential=essential,
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
