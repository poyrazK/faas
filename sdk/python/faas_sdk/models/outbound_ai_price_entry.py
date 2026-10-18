from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="OutboundAIPriceEntry")


@_attrs_define
class OutboundAIPriceEntry:
    """Price for model names starting with model_prefix ("*" matches any model; the longest prefix wins). Token prices are
    millicents per million tokens; absent cached, cache-write and input-image prices use the input price and an absent
    output-image price uses the output price. Media prices (ADR-997) are per image, per minute of input audio and per
    million input characters, and are zero when absent.

    """

    model_prefix: str
    input_millicents_per_mtok: int
    output_millicents_per_mtok: int
    cached_input_millicents_per_mtok: int | Unset = UNSET
    cache_write_millicents_per_mtok: int | Unset = UNSET
    input_image_millicents_per_mtok: int | Unset = UNSET
    output_image_millicents_per_mtok: int | Unset = UNSET
    millicents_per_image: int | Unset = UNSET
    """Charged per image when a call reports no output image tokens."""
    millicents_per_audio_minute: int | Unset = UNSET
    """Charged per minute of transcribed audio."""
    millicents_per_million_characters: int | Unset = UNSET
    """Charged per million characters of speech input."""
    millicents_per_video_second: int | Unset = UNSET
    """Charged per second of generated video (ADR-1432). A model_prefix of <model>@<resolution>, such as
    veo-3.1-generate-001@4k, prices that resolution apart."""
    millicents_per_web_search: int | Unset = UNSET
    """Charged per web search as the provider bills it: per call, query or grounded prompt (ADR-1433)."""
    millicents_per_file_search: int | Unset = UNSET
    """Charged per file search call (ADR-1433)."""
    millicents_per_code_session_gb: int | Unset = UNSET
    """Charged per GB of code-interpreter container memory per session (ADR-1433)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        model_prefix = self.model_prefix

        input_millicents_per_mtok = self.input_millicents_per_mtok

        output_millicents_per_mtok = self.output_millicents_per_mtok

        cached_input_millicents_per_mtok = self.cached_input_millicents_per_mtok

        cache_write_millicents_per_mtok = self.cache_write_millicents_per_mtok

        input_image_millicents_per_mtok = self.input_image_millicents_per_mtok

        output_image_millicents_per_mtok = self.output_image_millicents_per_mtok

        millicents_per_image = self.millicents_per_image

        millicents_per_audio_minute = self.millicents_per_audio_minute

        millicents_per_million_characters = self.millicents_per_million_characters

        millicents_per_video_second = self.millicents_per_video_second

        millicents_per_web_search = self.millicents_per_web_search

        millicents_per_file_search = self.millicents_per_file_search

        millicents_per_code_session_gb = self.millicents_per_code_session_gb

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "model_prefix": model_prefix,
                "input_millicents_per_mtok": input_millicents_per_mtok,
                "output_millicents_per_mtok": output_millicents_per_mtok,
            }
        )
        if cached_input_millicents_per_mtok is not UNSET:
            field_dict["cached_input_millicents_per_mtok"] = cached_input_millicents_per_mtok
        if cache_write_millicents_per_mtok is not UNSET:
            field_dict["cache_write_millicents_per_mtok"] = cache_write_millicents_per_mtok
        if input_image_millicents_per_mtok is not UNSET:
            field_dict["input_image_millicents_per_mtok"] = input_image_millicents_per_mtok
        if output_image_millicents_per_mtok is not UNSET:
            field_dict["output_image_millicents_per_mtok"] = output_image_millicents_per_mtok
        if millicents_per_image is not UNSET:
            field_dict["millicents_per_image"] = millicents_per_image
        if millicents_per_audio_minute is not UNSET:
            field_dict["millicents_per_audio_minute"] = millicents_per_audio_minute
        if millicents_per_million_characters is not UNSET:
            field_dict["millicents_per_million_characters"] = millicents_per_million_characters
        if millicents_per_video_second is not UNSET:
            field_dict["millicents_per_video_second"] = millicents_per_video_second
        if millicents_per_web_search is not UNSET:
            field_dict["millicents_per_web_search"] = millicents_per_web_search
        if millicents_per_file_search is not UNSET:
            field_dict["millicents_per_file_search"] = millicents_per_file_search
        if millicents_per_code_session_gb is not UNSET:
            field_dict["millicents_per_code_session_gb"] = millicents_per_code_session_gb

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        model_prefix = d.pop("model_prefix")

        input_millicents_per_mtok = d.pop("input_millicents_per_mtok")

        output_millicents_per_mtok = d.pop("output_millicents_per_mtok")

        cached_input_millicents_per_mtok = d.pop("cached_input_millicents_per_mtok", UNSET)

        cache_write_millicents_per_mtok = d.pop("cache_write_millicents_per_mtok", UNSET)

        input_image_millicents_per_mtok = d.pop("input_image_millicents_per_mtok", UNSET)

        output_image_millicents_per_mtok = d.pop("output_image_millicents_per_mtok", UNSET)

        millicents_per_image = d.pop("millicents_per_image", UNSET)

        millicents_per_audio_minute = d.pop("millicents_per_audio_minute", UNSET)

        millicents_per_million_characters = d.pop("millicents_per_million_characters", UNSET)

        millicents_per_video_second = d.pop("millicents_per_video_second", UNSET)

        millicents_per_web_search = d.pop("millicents_per_web_search", UNSET)

        millicents_per_file_search = d.pop("millicents_per_file_search", UNSET)

        millicents_per_code_session_gb = d.pop("millicents_per_code_session_gb", UNSET)

        outbound_ai_price_entry = cls(
            model_prefix=model_prefix,
            input_millicents_per_mtok=input_millicents_per_mtok,
            output_millicents_per_mtok=output_millicents_per_mtok,
            cached_input_millicents_per_mtok=cached_input_millicents_per_mtok,
            cache_write_millicents_per_mtok=cache_write_millicents_per_mtok,
            input_image_millicents_per_mtok=input_image_millicents_per_mtok,
            output_image_millicents_per_mtok=output_image_millicents_per_mtok,
            millicents_per_image=millicents_per_image,
            millicents_per_audio_minute=millicents_per_audio_minute,
            millicents_per_million_characters=millicents_per_million_characters,
            millicents_per_video_second=millicents_per_video_second,
            millicents_per_web_search=millicents_per_web_search,
            millicents_per_file_search=millicents_per_file_search,
            millicents_per_code_session_gb=millicents_per_code_session_gb,
        )

        outbound_ai_price_entry.additional_properties = d
        return outbound_ai_price_entry

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
