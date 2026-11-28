from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.outbound_ai_prompt import OutboundAIPrompt
    from ..models.outbound_ai_prompt_version import OutboundAIPromptVersion


T = TypeVar("T", bound="PushOutboundAIPromptVersionResponse")


@_attrs_define
class PushOutboundAIPromptVersionResponse:
    """The prompt and the version a push stored, or the identical latest version."""

    prompt: OutboundAIPrompt
    """A managed AI prompt. live_version serves unpinned calls; previous_live_version is what a rollback restores."""
    version: OutboundAIPromptVersion
    """One immutable version of a managed AI prompt."""
    created: bool
    """False when the push matched the latest version."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        prompt = self.prompt.to_dict()

        version = self.version.to_dict()

        created = self.created

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "prompt": prompt,
                "version": version,
                "created": created,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outbound_ai_prompt import OutboundAIPrompt
        from ..models.outbound_ai_prompt_version import OutboundAIPromptVersion

        d = dict(src_dict)
        prompt = OutboundAIPrompt.from_dict(d.pop("prompt"))

        version = OutboundAIPromptVersion.from_dict(d.pop("version"))

        created = d.pop("created")

        push_outbound_ai_prompt_version_response = cls(
            prompt=prompt,
            version=version,
            created=created,
        )

        push_outbound_ai_prompt_version_response.additional_properties = d
        return push_outbound_ai_prompt_version_response

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
