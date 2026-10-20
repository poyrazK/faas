from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_image_run_request_pull import CreateImageRunRequestPull, check_create_image_run_request_pull
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.create_image_run_request_env import CreateImageRunRequestEnv
    from ..models.image_run_registry_auth import ImageRunRegistryAuth


T = TypeVar("T", bound="CreateImageRunRequest")


@_attrs_define
class CreateImageRunRequest:
    """Body of POST /v1/image-runs (ADR-1161)."""

    image: str
    """OCI image reference; normalized before use (alpine → docker.io/library/alpine:latest)."""
    entrypoint: list[str] | Unset = UNSET
    """Replaces the image ENTRYPOINT; without args the image CMD is dropped."""
    args: list[str] | Unset = UNSET
    """Replaces the image CMD."""
    env: CreateImageRunRequestEnv | Unset = UNSET
    """Per-run environment, validated like Job run env_overrides."""
    ram_mb: int | Unset = UNSET
    """Task memory in MB; 0 means the Job default. Capped by the plan's Job RAM ceiling."""
    timeout_sec: int | Unset = UNSET
    """Task deadline in seconds; 0 means the plan's Job task timeout ceiling."""
    pull: CreateImageRunRequestPull | Unset = "missing"
    """always re-pulls a cached image so a moved tag is resolved again."""
    interactive: bool | Unset = UNSET
    """ADR-1330: hold the command until one client attaches to the run's task. The run has no retries and
    timeout_sec is at most 3600. Needs the operator gate FAAS_INTERACTIVE_IMAGE_RUNS; otherwise 501."""
    tty: bool | Unset = UNSET
    """Give the interactive command a pseudo-terminal. Requires interactive."""
    user: str | Unset = UNSET
    """ADR-1790: run as name|uid[:group|gid], resolved in the image's passwd and group files, instead of the image
    USER (docker run -u)."""
    workdir: str | Unset = UNSET
    """ADR-1790: absolute working directory instead of the image WORKDIR (docker run -w); created when missing."""
    registry_auth: ImageRunRegistryAuth | Unset = UNSET
    """ADR-1790: credential for a private image's registry. apid stores it sealed on the image's runner job for
    that registry host, exactly as PUT /v1/jobs/{name}/registry-credentials does, and never returns it. Every run
    that supplies it refreshes it."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        image = self.image

        entrypoint: list[str] | Unset = UNSET
        if not isinstance(self.entrypoint, Unset):
            entrypoint = self.entrypoint

        args: list[str] | Unset = UNSET
        if not isinstance(self.args, Unset):
            args = self.args

        env: dict[str, Any] | Unset = UNSET
        if not isinstance(self.env, Unset):
            env = self.env.to_dict()

        ram_mb = self.ram_mb

        timeout_sec = self.timeout_sec

        pull: str | Unset = UNSET
        if not isinstance(self.pull, Unset):
            pull = self.pull

        interactive = self.interactive

        tty = self.tty

        user = self.user

        workdir = self.workdir

        registry_auth: dict[str, Any] | Unset = UNSET
        if not isinstance(self.registry_auth, Unset):
            registry_auth = self.registry_auth.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "image": image,
            }
        )
        if entrypoint is not UNSET:
            field_dict["entrypoint"] = entrypoint
        if args is not UNSET:
            field_dict["args"] = args
        if env is not UNSET:
            field_dict["env"] = env
        if ram_mb is not UNSET:
            field_dict["ram_mb"] = ram_mb
        if timeout_sec is not UNSET:
            field_dict["timeout_sec"] = timeout_sec
        if pull is not UNSET:
            field_dict["pull"] = pull
        if interactive is not UNSET:
            field_dict["interactive"] = interactive
        if tty is not UNSET:
            field_dict["tty"] = tty
        if user is not UNSET:
            field_dict["user"] = user
        if workdir is not UNSET:
            field_dict["workdir"] = workdir
        if registry_auth is not UNSET:
            field_dict["registry_auth"] = registry_auth

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.create_image_run_request_env import CreateImageRunRequestEnv
        from ..models.image_run_registry_auth import ImageRunRegistryAuth

        d = dict(src_dict)
        image = d.pop("image")

        entrypoint = cast(list[str], d.pop("entrypoint", UNSET))

        args = cast(list[str], d.pop("args", UNSET))

        _env = d.pop("env", UNSET)
        env: CreateImageRunRequestEnv | Unset
        if isinstance(_env, Unset):
            env = UNSET
        else:
            env = CreateImageRunRequestEnv.from_dict(_env)

        ram_mb = d.pop("ram_mb", UNSET)

        timeout_sec = d.pop("timeout_sec", UNSET)

        _pull = d.pop("pull", UNSET)
        pull: CreateImageRunRequestPull | Unset
        if isinstance(_pull, Unset):
            pull = UNSET
        else:
            pull = check_create_image_run_request_pull(_pull)

        interactive = d.pop("interactive", UNSET)

        tty = d.pop("tty", UNSET)

        user = d.pop("user", UNSET)

        workdir = d.pop("workdir", UNSET)

        _registry_auth = d.pop("registry_auth", UNSET)
        registry_auth: ImageRunRegistryAuth | Unset
        if isinstance(_registry_auth, Unset):
            registry_auth = UNSET
        else:
            registry_auth = ImageRunRegistryAuth.from_dict(_registry_auth)

        create_image_run_request = cls(
            image=image,
            entrypoint=entrypoint,
            args=args,
            env=env,
            ram_mb=ram_mb,
            timeout_sec=timeout_sec,
            pull=pull,
            interactive=interactive,
            tty=tty,
            user=user,
            workdir=workdir,
            registry_auth=registry_auth,
        )

        create_image_run_request.additional_properties = d
        return create_image_run_request

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
