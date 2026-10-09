from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="DurableEntityRestoreResponse")


@_attrs_define
class DurableEntityRestoreResponse:
    version: int
    replayed: bool

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        replayed = self.replayed

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "version": version,
                "replayed": replayed,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        version = d.pop("version")

        replayed = d.pop("replayed")

        durable_entity_restore_response = cls(
            version=version,
            replayed=replayed,
        )

        return durable_entity_restore_response
