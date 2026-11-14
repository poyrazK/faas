from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.admit_data_api_shared_budget_body_version import (
    AdmitDataAPISharedBudgetBodyVersion,
    check_admit_data_api_shared_budget_body_version,
)

T = TypeVar("T", bound="AdmitDataAPISharedBudgetBody")


@_attrs_define
class AdmitDataAPISharedBudgetBody:
    version: AdmitDataAPISharedBudgetBodyVersion
    key: str
    requests: int
    window_seconds: int

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        key = self.key

        requests = self.requests

        window_seconds = self.window_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "version": version,
                "key": key,
                "requests": requests,
                "window_seconds": window_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        version = check_admit_data_api_shared_budget_body_version(d.pop("version"))

        key = d.pop("key")

        requests = d.pop("requests")

        window_seconds = d.pop("window_seconds")

        admit_data_api_shared_budget_body = cls(
            version=version,
            key=key,
            requests=requests,
            window_seconds=window_seconds,
        )

        return admit_data_api_shared_budget_body
