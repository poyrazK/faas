from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="OutboundAISemanticCache")


@_attrs_define
class OutboundAISemanticCache:
    """Answer a call from the response cache when its last user turn means the same as a cached call's and everything else
    matches exactly. Pro and Scale plans; needs ai_provider and a response cache TTL. The embedding integration, one of
    the account's enabled OpenAI or Gemini integrations, embeds each candidate call on its own key, metered under the
    caller semantic_cache. A hit carries X-Gregale-AI-Cache semantic and X-Gregale-AI-Similarity.

    """

    enabled: bool
    embedding_integration_id: UUID
    embedding_model: str
    threshold_basis_points: int | Unset = UNSET
    """Cosine similarity a hit needs, in basis points. Absent or 0 means 9500."""
    share_across_users: bool | Unset = UNSET
    """Let calls of different end users (X-Gregale-AI-User) share answers. Off, each end user has their own."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        embedding_integration_id = str(self.embedding_integration_id)

        embedding_model = self.embedding_model

        threshold_basis_points = self.threshold_basis_points

        share_across_users = self.share_across_users

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "enabled": enabled,
                "embedding_integration_id": embedding_integration_id,
                "embedding_model": embedding_model,
            }
        )
        if threshold_basis_points is not UNSET:
            field_dict["threshold_basis_points"] = threshold_basis_points
        if share_across_users is not UNSET:
            field_dict["share_across_users"] = share_across_users

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        enabled = d.pop("enabled")

        embedding_integration_id = UUID(d.pop("embedding_integration_id"))

        embedding_model = d.pop("embedding_model")

        threshold_basis_points = d.pop("threshold_basis_points", UNSET)

        share_across_users = d.pop("share_across_users", UNSET)

        outbound_ai_semantic_cache = cls(
            enabled=enabled,
            embedding_integration_id=embedding_integration_id,
            embedding_model=embedding_model,
            threshold_basis_points=threshold_basis_points,
            share_across_users=share_across_users,
        )

        outbound_ai_semantic_cache.additional_properties = d
        return outbound_ai_semantic_cache

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
