from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.env_diff_kind import EnvDiffKind, check_env_diff_kind

if TYPE_CHECKING:
    from ..models.env_diff_row_cells import EnvDiffRowCells


T = TypeVar("T", bound="EnvDiffRow")


@_attrs_define
class EnvDiffRow:
    """One (key, kind) row in the env-diff matrix. The Cells map is keyed by scope."""

    key: str
    kind: EnvDiffKind
    """Discriminator for an env-diff matrix row. 'secret' rows carry {present, value_hash}; 'env' rows carry
    {present, value}. The cell shape is uniform but the field population is kind-aware."""
    cells: EnvDiffRowCells
    """scope → cell. The handler populates the unioned set of scopes; consumers iterate EnvDiffResponse.Scopes for
    the canonical order."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        kind: str = self.kind

        cells = self.cells.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "key": key,
                "kind": kind,
                "cells": cells,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.env_diff_row_cells import EnvDiffRowCells

        d = dict(src_dict)
        key = d.pop("key")

        kind = check_env_diff_kind(d.pop("kind"))

        cells = EnvDiffRowCells.from_dict(d.pop("cells"))

        env_diff_row = cls(
            key=key,
            kind=kind,
            cells=cells,
        )

        env_diff_row.additional_properties = d
        return env_diff_row

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
