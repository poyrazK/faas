from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_readiness_attention_receipt import MCPReadinessAttentionReceipt


T = TypeVar("T", bound="MCPReadinessAttentionHistory")


@_attrs_define
class MCPReadinessAttentionHistory:
    """Bounded immutable history of incident attention decisions."""

    actions: list[MCPReadinessAttentionReceipt]
    next_before: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        actions = []
        for actions_item_data in self.actions:
            actions_item = actions_item_data.to_dict()
            actions.append(actions_item)

        next_before = self.next_before

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "actions": actions,
            }
        )
        if next_before is not UNSET:
            field_dict["next_before"] = next_before

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_readiness_attention_receipt import MCPReadinessAttentionReceipt

        d = dict(src_dict)
        actions = []
        _actions = d.pop("actions")
        for actions_item_data in _actions:
            actions_item = MCPReadinessAttentionReceipt.from_dict(actions_item_data)

            actions.append(actions_item)

        next_before = d.pop("next_before", UNSET)

        mcp_readiness_attention_history = cls(
            actions=actions,
            next_before=next_before,
        )

        mcp_readiness_attention_history.additional_properties = d
        return mcp_readiness_attention_history

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
