from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="DataPipelineFreshnessRequest")


@_attrs_define
class DataPipelineFreshnessRequest:
    """Revision-fenced freshness configuration. Zero disables the target; enabled targets range from 15 to 86400 whole
    seconds. Existing health webhook destinations receive debounced freshness_unconfirmed incidents.

    """

    expected_revision: int
    target_seconds: int
    """DataPipelineFreshnessRequest uses zero to disable confirmation alerts; enabled targets must be at least 15
    seconds."""

    def to_dict(self) -> dict[str, Any]:
        expected_revision = self.expected_revision

        target_seconds = self.target_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_revision": expected_revision,
                "target_seconds": target_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_revision = d.pop("expected_revision")

        target_seconds = d.pop("target_seconds")

        data_pipeline_freshness_request = cls(
            expected_revision=expected_revision,
            target_seconds=target_seconds,
        )

        return data_pipeline_freshness_request
