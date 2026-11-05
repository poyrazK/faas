from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.outbound_ai_moderation_action import OutboundAIModerationAction, check_outbound_ai_moderation_action
from ..models.outbound_ai_moderation_categories_item import (
    OutboundAIModerationCategoriesItem,
    check_outbound_ai_moderation_categories_item,
)
from ..models.outbound_ai_moderation_kind import OutboundAIModerationKind, check_outbound_ai_moderation_kind
from ..models.outbound_ai_moderation_on_error import OutboundAIModerationOnError, check_outbound_ai_moderation_on_error
from ..types import UNSET, Unset

T = TypeVar("T", bound="OutboundAIModeration")


@_attrs_define
class OutboundAIModeration:
    """A model-based check of the request's user, tool and input text before the provider call. It runs through another of
    the account's AI integrations and is billed by that integration's provider: openai_moderation calls OpenAI's
    /v1/moderations on an OpenAI integration; classifier asks a chat model which categories apply. flag lets the call
    through and names the categories in X-Gregale-AI-Moderation; block refuses it with 400 outbound_ai_content_flagged.
    on_error decides what a failed check does; block answers 503 outbound_ai_moderation_unavailable.

    """

    action: OutboundAIModerationAction | Unset = UNSET
    kind: OutboundAIModerationKind | Unset = UNSET
    integration_id: UUID | Unset = UNSET
    model: str | Unset = UNSET
    """Required for classifier."""
    categories: list[OutboundAIModerationCategoriesItem] | Unset = UNSET
    on_error: OutboundAIModerationOnError | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        action: str | Unset = UNSET
        if not isinstance(self.action, Unset):
            action = self.action

        kind: str | Unset = UNSET
        if not isinstance(self.kind, Unset):
            kind = self.kind

        integration_id: str | Unset = UNSET
        if not isinstance(self.integration_id, Unset):
            integration_id = str(self.integration_id)

        model = self.model

        categories: list[str] | Unset = UNSET
        if not isinstance(self.categories, Unset):
            categories = []
            for categories_item_data in self.categories:
                categories_item: str = categories_item_data
                categories.append(categories_item)

        on_error: str | Unset = UNSET
        if not isinstance(self.on_error, Unset):
            on_error = self.on_error

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if action is not UNSET:
            field_dict["action"] = action
        if kind is not UNSET:
            field_dict["kind"] = kind
        if integration_id is not UNSET:
            field_dict["integration_id"] = integration_id
        if model is not UNSET:
            field_dict["model"] = model
        if categories is not UNSET:
            field_dict["categories"] = categories
        if on_error is not UNSET:
            field_dict["on_error"] = on_error

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _action = d.pop("action", UNSET)
        action: OutboundAIModerationAction | Unset
        if isinstance(_action, Unset):
            action = UNSET
        else:
            action = check_outbound_ai_moderation_action(_action)

        _kind = d.pop("kind", UNSET)
        kind: OutboundAIModerationKind | Unset
        if isinstance(_kind, Unset):
            kind = UNSET
        else:
            kind = check_outbound_ai_moderation_kind(_kind)

        _integration_id = d.pop("integration_id", UNSET)
        integration_id: UUID | Unset
        if isinstance(_integration_id, Unset):
            integration_id = UNSET
        else:
            integration_id = UUID(_integration_id)

        model = d.pop("model", UNSET)

        _categories = d.pop("categories", UNSET)
        categories: list[OutboundAIModerationCategoriesItem] | Unset = UNSET
        if _categories is not UNSET:
            categories = []
            for categories_item_data in _categories:
                categories_item = check_outbound_ai_moderation_categories_item(categories_item_data)

                categories.append(categories_item)

        _on_error = d.pop("on_error", UNSET)
        on_error: OutboundAIModerationOnError | Unset
        if isinstance(_on_error, Unset):
            on_error = UNSET
        else:
            on_error = check_outbound_ai_moderation_on_error(_on_error)

        outbound_ai_moderation = cls(
            action=action,
            kind=kind,
            integration_id=integration_id,
            model=model,
            categories=categories,
            on_error=on_error,
        )

        outbound_ai_moderation.additional_properties = d
        return outbound_ai_moderation

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
