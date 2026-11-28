from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.compose_process_stop_signal import ComposeProcessStopSignal, check_compose_process_stop_signal
from ..types import UNSET, Unset

T = TypeVar("T", bound="ComposeProcess")


@_attrs_define
class ComposeProcess:
    """Compose process overrides for a prebuilt image. Omitted fields inherit the image's ENTRYPOINT, USER, WORKDIR and
    stop settings.

    """

    entrypoint: list[str] | Unset = UNSET
    """Replaces the image ENTRYPOINT and drops its CMD; the Compose command supplies the arguments. An empty array
    clears the ENTRYPOINT."""
    user: str | Unset = UNSET
    """User name or id, optionally with :group, resolved against the image's /etc/passwd and /etc/group."""
    working_dir: str | Unset = UNSET
    """Clean absolute working directory."""
    stop_signal: ComposeProcessStopSignal | Unset = UNSET
    stop_grace_period_s: int | Unset = UNSET
    """Seconds between the stop signal and SIGKILL, capped by the plan."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        entrypoint: list[str] | Unset = UNSET
        if not isinstance(self.entrypoint, Unset):
            entrypoint = self.entrypoint

        user = self.user

        working_dir = self.working_dir

        stop_signal: str | Unset = UNSET
        if not isinstance(self.stop_signal, Unset):
            stop_signal = self.stop_signal

        stop_grace_period_s = self.stop_grace_period_s

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if entrypoint is not UNSET:
            field_dict["entrypoint"] = entrypoint
        if user is not UNSET:
            field_dict["user"] = user
        if working_dir is not UNSET:
            field_dict["working_dir"] = working_dir
        if stop_signal is not UNSET:
            field_dict["stop_signal"] = stop_signal
        if stop_grace_period_s is not UNSET:
            field_dict["stop_grace_period_s"] = stop_grace_period_s

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        entrypoint = cast(list[str], d.pop("entrypoint", UNSET))

        user = d.pop("user", UNSET)

        working_dir = d.pop("working_dir", UNSET)

        _stop_signal = d.pop("stop_signal", UNSET)
        stop_signal: ComposeProcessStopSignal | Unset
        if isinstance(_stop_signal, Unset):
            stop_signal = UNSET
        else:
            stop_signal = check_compose_process_stop_signal(_stop_signal)

        stop_grace_period_s = d.pop("stop_grace_period_s", UNSET)

        compose_process = cls(
            entrypoint=entrypoint,
            user=user,
            working_dir=working_dir,
            stop_signal=stop_signal,
            stop_grace_period_s=stop_grace_period_s,
        )

        compose_process.additional_properties = d
        return compose_process

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
