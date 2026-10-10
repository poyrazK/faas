from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.pre_auth_enforcement_suggestion_status import (
    PreAuthEnforcementSuggestionStatus,
    check_pre_auth_enforcement_suggestion_status,
)

T = TypeVar("T", bound="PreAuthEnforcementSuggestion")


@_attrs_define
class PreAuthEnforcementSuggestion:
    """Advice on switching an observe-mode guard to enforce, judged on the
    response range (ADR-829 amendment 1). Present only for observe mode
    with a healthy metrics source; nothing is applied automatically.
    `ready` needs a range of 24h or longer, at least 1000 requests, and no
    successful (2xx/3xx) request among those the guard would have blocked.

    """

    status: PreAuthEnforcementSuggestionStatus
    reason: str
    requests: int
    """Every gateway request to the app in the range."""
    would_block: int
    """Sum over the app and route policies."""
    would_block_succeeded: int
    """Would-block requests whose final response was 2xx or 3xx."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        reason = self.reason

        requests = self.requests

        would_block = self.would_block

        would_block_succeeded = self.would_block_succeeded

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "reason": reason,
                "requests": requests,
                "would_block": would_block,
                "would_block_succeeded": would_block_succeeded,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_pre_auth_enforcement_suggestion_status(d.pop("status"))

        reason = d.pop("reason")

        requests = d.pop("requests")

        would_block = d.pop("would_block")

        would_block_succeeded = d.pop("would_block_succeeded")

        pre_auth_enforcement_suggestion = cls(
            status=status,
            reason=reason,
            requests=requests,
            would_block=would_block,
            would_block_succeeded=would_block_succeeded,
        )

        pre_auth_enforcement_suggestion.additional_properties = d
        return pre_auth_enforcement_suggestion

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
