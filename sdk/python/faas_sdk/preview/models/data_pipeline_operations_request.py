from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.data_pipeline_operations_request_action import (
    DataPipelineOperationsRequestAction,
    check_data_pipeline_operations_request_action,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.data_pipeline_operations import DataPipelineOperations


T = TypeVar("T", bound="DataPipelineOperationsRequest")


@_attrs_define
class DataPipelineOperationsRequest:
    """Revision-checked replacement maintenance configuration or an explicit snapshot/quality run request. Configure
    supplies config; run actions omit it and still respect the saved UTC window.

    """

    expected_revision: int
    action: DataPipelineOperationsRequestAction
    config: DataPipelineOperations | Unset = UNSET
    """Versioned policy for bounded scheduled snapshots, mirror quality checks and optional maintenance. Snapshot
    mode is chosen at creation and cannot be changed later."""

    def to_dict(self) -> dict[str, Any]:
        expected_revision = self.expected_revision

        action: str = self.action

        config: dict[str, Any] | Unset = UNSET
        if not isinstance(self.config, Unset):
            config = self.config.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_revision": expected_revision,
                "action": action,
            }
        )
        if config is not UNSET:
            field_dict["config"] = config

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_pipeline_operations import DataPipelineOperations

        d = dict(src_dict)
        expected_revision = d.pop("expected_revision")

        action = check_data_pipeline_operations_request_action(d.pop("action"))

        _config = d.pop("config", UNSET)
        config: DataPipelineOperations | Unset
        if isinstance(_config, Unset):
            config = UNSET
        else:
            config = DataPipelineOperations.from_dict(_config)

        data_pipeline_operations_request = cls(
            expected_revision=expected_revision,
            action=action,
            config=config,
        )

        return data_pipeline_operations_request
