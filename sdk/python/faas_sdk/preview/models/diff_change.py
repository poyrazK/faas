from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.diff_change_kind import DiffChangeKind, check_diff_change_kind
from ..types import UNSET, Unset

T = TypeVar("T", bound="DiffChange")


@_attrs_define
class DiffChange:
    """One diff row. Polymorphic values (Before / After) re-emitted
    as JSON objects — the engine emits primitives, slices, or
    structs depending on the field.

    """

    field: str
    """Human path: 'memory', 'concurrency', 'environment.<scope>.<key>', 'cron[<schedule> <path>]',
    'edge_rule[<kind> <host><path>]'"""
    kind: DiffChangeKind
    before: Any | Unset = UNSET
    """Primitive value (int / string / bool / []string) — JSON-encoded. Omitted on Add."""
    after: Any | Unset = UNSET
    """Primitive value — JSON-encoded. Omitted on Remove."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        field = self.field

        kind: str = self.kind

        before = self.before

        after = self.after

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "field": field,
                "kind": kind,
            }
        )
        if before is not UNSET:
            field_dict["before"] = before
        if after is not UNSET:
            field_dict["after"] = after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        field = d.pop("field")

        kind = check_diff_change_kind(d.pop("kind"))

        before = d.pop("before", UNSET)

        after = d.pop("after", UNSET)

        diff_change = cls(
            field=field,
            kind=kind,
            before=before,
            after=after,
        )

        diff_change.additional_properties = d
        return diff_change

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
