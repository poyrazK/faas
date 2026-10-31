from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_retry_spec_backoff import WorkflowRetrySpecBackoff, check_workflow_retry_spec_backoff
from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowRetrySpec")


@_attrs_define
class WorkflowRetrySpec:
    """Retry policy for one workflow step."""

    max_attempts: int
    backoff: WorkflowRetrySpecBackoff | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        max_attempts = self.max_attempts

        backoff: str | Unset = UNSET
        if not isinstance(self.backoff, Unset):
            backoff = self.backoff

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "max_attempts": max_attempts,
            }
        )
        if backoff is not UNSET:
            field_dict["backoff"] = backoff

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        max_attempts = d.pop("max_attempts")

        _backoff = d.pop("backoff", UNSET)
        backoff: WorkflowRetrySpecBackoff | Unset
        if isinstance(_backoff, Unset):
            backoff = UNSET
        else:
            backoff = check_workflow_retry_spec_backoff(_backoff)

        workflow_retry_spec = cls(
            max_attempts=max_attempts,
            backoff=backoff,
        )

        workflow_retry_spec.additional_properties = d
        return workflow_retry_spec

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
