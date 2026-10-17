from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="OutboundAIUsageItem")


@_attrs_define
class OutboundAIUsageItem:
    """One UTC-day rollup for a caller and model. input_tokens includes cached reads and cache writes, which are also
    reported separately.

    """

    caller: str
    """App UUID, "runs" for stateless Runs, or "replay" for replayed calls (ADR-997)."""
    model: str
    """Provider-reported model; "other" collects models beyond the daily distinct-model cap and "unknown" covers
    responses without one."""
    requests: int
    error_requests: int
    usage_unknown_requests: int
    """Successful calls whose response reported no usage, such as an OpenAI stream without
    stream_options.include_usage."""
    input_tokens: int
    cached_input_tokens: int
    cache_write_tokens: int
    output_tokens: int
    images: int
    """Images billed per image (ADR-997)."""
    audio_seconds: int
    """Seconds of transcribed audio."""
    characters: int
    """Characters of speech input."""
    video_seconds: int
    """Seconds of generated video (ADR-1370)."""
    web_searches: int
    """Web searches as the provider bills them: per call, query or grounded prompt (ADR-1380)."""
    file_searches: int
    """File search calls (ADR-1380)."""
    code_session_gb: int
    """GB of code-interpreter container sessions (ADR-1380)."""
    cached_requests: int
    """Requests the response cache served, with no tokens or cost (ADR-1440)."""
    cache_saved_millicents: int
    """Provider cost the response cache avoided, priced from the cached responses at the current price list."""
    prompt_cache_saved_millicents: int
    """What provider prompt caching saved against plain input prices; negative when cache writes outweighed reads."""
    cost_millicents: int | Unset = UNSET
    """Priced from the list in effect at each call, else the built-in catalog, and rounded up per call; zero for an
    unpriced model."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        caller = self.caller

        model = self.model

        requests = self.requests

        error_requests = self.error_requests

        usage_unknown_requests = self.usage_unknown_requests

        input_tokens = self.input_tokens

        cached_input_tokens = self.cached_input_tokens

        cache_write_tokens = self.cache_write_tokens

        output_tokens = self.output_tokens

        images = self.images

        audio_seconds = self.audio_seconds

        characters = self.characters

        video_seconds = self.video_seconds

        web_searches = self.web_searches

        file_searches = self.file_searches

        code_session_gb = self.code_session_gb

        cached_requests = self.cached_requests

        cache_saved_millicents = self.cache_saved_millicents

        prompt_cache_saved_millicents = self.prompt_cache_saved_millicents

        cost_millicents = self.cost_millicents

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "caller": caller,
                "model": model,
                "requests": requests,
                "error_requests": error_requests,
                "usage_unknown_requests": usage_unknown_requests,
                "input_tokens": input_tokens,
                "cached_input_tokens": cached_input_tokens,
                "cache_write_tokens": cache_write_tokens,
                "output_tokens": output_tokens,
                "images": images,
                "audio_seconds": audio_seconds,
                "characters": characters,
                "video_seconds": video_seconds,
                "web_searches": web_searches,
                "file_searches": file_searches,
                "code_session_gb": code_session_gb,
                "cached_requests": cached_requests,
                "cache_saved_millicents": cache_saved_millicents,
                "prompt_cache_saved_millicents": prompt_cache_saved_millicents,
            }
        )
        if cost_millicents is not UNSET:
            field_dict["cost_millicents"] = cost_millicents

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        caller = d.pop("caller")

        model = d.pop("model")

        requests = d.pop("requests")

        error_requests = d.pop("error_requests")

        usage_unknown_requests = d.pop("usage_unknown_requests")

        input_tokens = d.pop("input_tokens")

        cached_input_tokens = d.pop("cached_input_tokens")

        cache_write_tokens = d.pop("cache_write_tokens")

        output_tokens = d.pop("output_tokens")

        images = d.pop("images")

        audio_seconds = d.pop("audio_seconds")

        characters = d.pop("characters")

        video_seconds = d.pop("video_seconds")

        web_searches = d.pop("web_searches")

        file_searches = d.pop("file_searches")

        code_session_gb = d.pop("code_session_gb")

        cached_requests = d.pop("cached_requests")

        cache_saved_millicents = d.pop("cache_saved_millicents")

        prompt_cache_saved_millicents = d.pop("prompt_cache_saved_millicents")

        cost_millicents = d.pop("cost_millicents", UNSET)

        outbound_ai_usage_item = cls(
            caller=caller,
            model=model,
            requests=requests,
            error_requests=error_requests,
            usage_unknown_requests=usage_unknown_requests,
            input_tokens=input_tokens,
            cached_input_tokens=cached_input_tokens,
            cache_write_tokens=cache_write_tokens,
            output_tokens=output_tokens,
            images=images,
            audio_seconds=audio_seconds,
            characters=characters,
            video_seconds=video_seconds,
            web_searches=web_searches,
            file_searches=file_searches,
            code_session_gb=code_session_gb,
            cached_requests=cached_requests,
            cache_saved_millicents=cache_saved_millicents,
            prompt_cache_saved_millicents=prompt_cache_saved_millicents,
            cost_millicents=cost_millicents,
        )

        outbound_ai_usage_item.additional_properties = d
        return outbound_ai_usage_item

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
