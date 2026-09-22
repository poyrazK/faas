from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="EdgeRuleAsyncAction")


@_attrs_define
class EdgeRuleAsyncAction:
    """Marks the matched route for durable asynchronous execution (ADR-215).
    The action is an empty object; retry, deadline, retention, and payload
    limits come from the existing invocation and account-plan contracts.

    """

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        edge_rule_async_action = cls()

        return edge_rule_async_action
