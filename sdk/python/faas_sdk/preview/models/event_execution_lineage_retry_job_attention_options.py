from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.event_execution_lineage_retry_job_attention_options_attention import (
    EventExecutionLineageRetryJobAttentionOptionsAttention,
    check_event_execution_lineage_retry_job_attention_options_attention,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventExecutionLineageRetryJobAttentionOptions")


@_attrs_define
class EventExecutionLineageRetryJobAttentionOptions:
    """Warning filter selecting retained retry jobs with inactivity or retention pressure."""

    attention: EventExecutionLineageRetryJobAttentionOptionsAttention | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        attention: str | Unset = UNSET
        if not isinstance(self.attention, Unset):
            attention = self.attention

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if attention is not UNSET:
            field_dict["attention"] = attention

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _attention = d.pop("attention", UNSET)
        attention: EventExecutionLineageRetryJobAttentionOptionsAttention | Unset
        if isinstance(_attention, Unset):
            attention = UNSET
        else:
            attention = check_event_execution_lineage_retry_job_attention_options_attention(_attention)

        event_execution_lineage_retry_job_attention_options = cls(
            attention=attention,
        )

        return event_execution_lineage_retry_job_attention_options
