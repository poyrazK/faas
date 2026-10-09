from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.durable_entity_retry_response_target import (
    DurableEntityRetryResponseTarget,
    check_durable_entity_retry_response_target,
)

T = TypeVar("T", bound="DurableEntityRetryResponse")


@_attrs_define
class DurableEntityRetryResponse:
    version: int
    target: DurableEntityRetryResponseTarget
    rearmed: bool
    """Retry metadata was reset; no execution or receiver completion is implied."""

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        target: str = self.target

        rearmed = self.rearmed

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "version": version,
                "target": target,
                "rearmed": rearmed,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        version = d.pop("version")

        target = check_durable_entity_retry_response_target(d.pop("target"))

        rearmed = d.pop("rearmed")

        durable_entity_retry_response = cls(
            version=version,
            target=target,
            rearmed=rearmed,
        )

        return durable_entity_retry_response
