from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.environment_git_source_update_mode import (
    EnvironmentGitSourceUpdateMode,
    check_environment_git_source_update_mode,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EnvironmentGitSourceUpdate")


@_attrs_define
class EnvironmentGitSourceUpdate:
    """Change controls with a generation fence; suspended sources keep ownership."""

    expected_generation: int
    mode: EnvironmentGitSourceUpdateMode | Unset = UNSET
    prune: bool | Unset = UNSET
    suspended: bool | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        expected_generation = self.expected_generation

        mode: str | Unset = UNSET
        if not isinstance(self.mode, Unset):
            mode = self.mode

        prune = self.prune

        suspended = self.suspended

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_generation": expected_generation,
            }
        )
        if mode is not UNSET:
            field_dict["mode"] = mode
        if prune is not UNSET:
            field_dict["prune"] = prune
        if suspended is not UNSET:
            field_dict["suspended"] = suspended

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_generation = d.pop("expected_generation")

        _mode = d.pop("mode", UNSET)
        mode: EnvironmentGitSourceUpdateMode | Unset
        if isinstance(_mode, Unset):
            mode = UNSET
        else:
            mode = check_environment_git_source_update_mode(_mode)

        prune = d.pop("prune", UNSET)

        suspended = d.pop("suspended", UNSET)

        environment_git_source_update = cls(
            expected_generation=expected_generation,
            mode=mode,
            prune=prune,
            suspended=suspended,
        )

        return environment_git_source_update
