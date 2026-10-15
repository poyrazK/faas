from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.outbound_ai_content_policy_email import (
    OutboundAIContentPolicyEmail,
    check_outbound_ai_content_policy_email,
)
from ..models.outbound_ai_content_policy_iban import OutboundAIContentPolicyIban, check_outbound_ai_content_policy_iban
from ..models.outbound_ai_content_policy_payment_card import (
    OutboundAIContentPolicyPaymentCard,
    check_outbound_ai_content_policy_payment_card,
)
from ..models.outbound_ai_content_policy_secret import (
    OutboundAIContentPolicySecret,
    check_outbound_ai_content_policy_secret,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.outbound_ai_moderation import OutboundAIModeration


T = TypeVar("T", bound="OutboundAIContentPolicy")


@_attrs_define
class OutboundAIContentPolicy:
    """An integration's content guardrails (ADR-997). Each detector is off, redacts its findings before the request reaches
    the provider, or blocks the request. A request containing a blocked term, compared without case, is blocked. The
    gateway checks the string values of JSON bodies and the text fields of forms.

    """

    email: OutboundAIContentPolicyEmail
    payment_card: OutboundAIContentPolicyPaymentCard
    iban: OutboundAIContentPolicyIban
    secret: OutboundAIContentPolicySecret
    """API keys, tokens and private keys."""
    blocked_terms: list[str]
    moderation: OutboundAIModeration | Unset = UNSET
    """A model-based check of the request's user, tool and input text before the provider call (ADR-1340). It runs
    through another of the account's AI integrations and is billed by that integration's provider: openai_moderation
    calls OpenAI's /v1/moderations on an OpenAI integration; classifier asks a chat model which categories apply.
    flag lets the call through and names the categories in X-Gregale-AI-Moderation; block refuses it with 400
    outbound_ai_content_flagged. on_error decides what a failed check does; block answers 503
    outbound_ai_moderation_unavailable."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        email: str = self.email

        payment_card: str = self.payment_card

        iban: str = self.iban

        secret: str = self.secret

        blocked_terms = self.blocked_terms

        moderation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.moderation, Unset):
            moderation = self.moderation.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "email": email,
                "payment_card": payment_card,
                "iban": iban,
                "secret": secret,
                "blocked_terms": blocked_terms,
            }
        )
        if moderation is not UNSET:
            field_dict["moderation"] = moderation

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outbound_ai_moderation import OutboundAIModeration

        d = dict(src_dict)
        email = check_outbound_ai_content_policy_email(d.pop("email"))

        payment_card = check_outbound_ai_content_policy_payment_card(d.pop("payment_card"))

        iban = check_outbound_ai_content_policy_iban(d.pop("iban"))

        secret = check_outbound_ai_content_policy_secret(d.pop("secret"))

        blocked_terms = cast(list[str], d.pop("blocked_terms"))

        _moderation = d.pop("moderation", UNSET)
        moderation: OutboundAIModeration | Unset
        if isinstance(_moderation, Unset):
            moderation = UNSET
        else:
            moderation = OutboundAIModeration.from_dict(_moderation)

        outbound_ai_content_policy = cls(
            email=email,
            payment_card=payment_card,
            iban=iban,
            secret=secret,
            blocked_terms=blocked_terms,
            moderation=moderation,
        )

        outbound_ai_content_policy.additional_properties = d
        return outbound_ai_content_policy

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
