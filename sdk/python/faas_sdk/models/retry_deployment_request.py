from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.retry_deployment_request_from_stage import (
    RetryDeploymentRequestFromStage,
    check_retry_deployment_request_from_stage,
)

T = TypeVar("T", bound="RetryDeploymentRequest")


@_attrs_define
class RetryDeploymentRequest:
    """Body for POST /v1/deployments/{id}/retry. Identifies the stage the retry should resume from. The closed-6 vocabulary
    mirrors `state.AllStageNames` (ADR-117); the API rejects unknown values with 400. Empty strings are rejected for the
    same reason.

    """

    from_stage: RetryDeploymentRequestFromStage
    """Closed-6 stage vocabulary. `source_download` re-runs the whole pipeline (intentional retry-from-top); any
    other value resumes from that stage with all prior inputs copied from the source row."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from_stage: str = self.from_stage

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "from_stage": from_stage,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        from_stage = check_retry_deployment_request_from_stage(d.pop("from_stage"))

        retry_deployment_request = cls(
            from_stage=from_stage,
        )

        retry_deployment_request.additional_properties = d
        return retry_deployment_request

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
