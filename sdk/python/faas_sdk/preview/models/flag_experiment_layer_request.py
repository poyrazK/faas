from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="FlagExperimentLayerRequest")


@_attrs_define
class FlagExperimentLayerRequest:
    """A layer to create."""

    key: str

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "key": key,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        key = d.pop("key")

        flag_experiment_layer_request = cls(
            key=key,
        )

        return flag_experiment_layer_request
