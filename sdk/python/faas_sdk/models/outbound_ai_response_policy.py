from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.outbound_ai_response_policy_blocked_terms import (
    OutboundAIResponsePolicyBlockedTerms,
    check_outbound_ai_response_policy_blocked_terms,
)
from ..models.outbound_ai_response_policy_email import (
    OutboundAIResponsePolicyEmail,
    check_outbound_ai_response_policy_email,
)
from ..models.outbound_ai_response_policy_iban import (
    OutboundAIResponsePolicyIban,
    check_outbound_ai_response_policy_iban,
)
from ..models.outbound_ai_response_policy_moderation import (
    OutboundAIResponsePolicyModeration,
    check_outbound_ai_response_policy_moderation,
)
from ..models.outbound_ai_response_policy_payment_card import (
    OutboundAIResponsePolicyPaymentCard,
    check_outbound_ai_response_policy_payment_card,
)
from ..models.outbound_ai_response_policy_secret import (
    OutboundAIResponsePolicySecret,
    check_outbound_ai_response_policy_secret,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OutboundAIResponsePolicy")


@_attrs_define
class OutboundAIResponsePolicy:
    """Checks of the text a model sends back, before the caller sees it. The detectors redact or block as on requests;
    blocked_terms block refuses a response that contains one of the policy's blocked terms; moderation runs the policy's
    moderation check on the response, without prompt_injection, and flags or blocks it. A refused whole response answers
    422 outbound_ai_response_blocked or outbound_ai_response_flagged; a refused stream ends with an error event. Tool
    call arguments and reasoning text are not checked.

    """

    email: OutboundAIResponsePolicyEmail | Unset = UNSET
    payment_card: OutboundAIResponsePolicyPaymentCard | Unset = UNSET
    iban: OutboundAIResponsePolicyIban | Unset = UNSET
    secret: OutboundAIResponsePolicySecret | Unset = UNSET
    blocked_terms: OutboundAIResponsePolicyBlockedTerms | Unset = UNSET
    moderation: OutboundAIResponsePolicyModeration | Unset = UNSET
    """Uses the policy's moderation check settings, which must be complete even while moderation.action is off."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        email: str | Unset = UNSET
        if not isinstance(self.email, Unset):
            email = self.email

        payment_card: str | Unset = UNSET
        if not isinstance(self.payment_card, Unset):
            payment_card = self.payment_card

        iban: str | Unset = UNSET
        if not isinstance(self.iban, Unset):
            iban = self.iban

        secret: str | Unset = UNSET
        if not isinstance(self.secret, Unset):
            secret = self.secret

        blocked_terms: str | Unset = UNSET
        if not isinstance(self.blocked_terms, Unset):
            blocked_terms = self.blocked_terms

        moderation: str | Unset = UNSET
        if not isinstance(self.moderation, Unset):
            moderation = self.moderation

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if email is not UNSET:
            field_dict["email"] = email
        if payment_card is not UNSET:
            field_dict["payment_card"] = payment_card
        if iban is not UNSET:
            field_dict["iban"] = iban
        if secret is not UNSET:
            field_dict["secret"] = secret
        if blocked_terms is not UNSET:
            field_dict["blocked_terms"] = blocked_terms
        if moderation is not UNSET:
            field_dict["moderation"] = moderation

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _email = d.pop("email", UNSET)
        email: OutboundAIResponsePolicyEmail | Unset
        if isinstance(_email, Unset):
            email = UNSET
        else:
            email = check_outbound_ai_response_policy_email(_email)

        _payment_card = d.pop("payment_card", UNSET)
        payment_card: OutboundAIResponsePolicyPaymentCard | Unset
        if isinstance(_payment_card, Unset):
            payment_card = UNSET
        else:
            payment_card = check_outbound_ai_response_policy_payment_card(_payment_card)

        _iban = d.pop("iban", UNSET)
        iban: OutboundAIResponsePolicyIban | Unset
        if isinstance(_iban, Unset):
            iban = UNSET
        else:
            iban = check_outbound_ai_response_policy_iban(_iban)

        _secret = d.pop("secret", UNSET)
        secret: OutboundAIResponsePolicySecret | Unset
        if isinstance(_secret, Unset):
            secret = UNSET
        else:
            secret = check_outbound_ai_response_policy_secret(_secret)

        _blocked_terms = d.pop("blocked_terms", UNSET)
        blocked_terms: OutboundAIResponsePolicyBlockedTerms | Unset
        if isinstance(_blocked_terms, Unset):
            blocked_terms = UNSET
        else:
            blocked_terms = check_outbound_ai_response_policy_blocked_terms(_blocked_terms)

        _moderation = d.pop("moderation", UNSET)
        moderation: OutboundAIResponsePolicyModeration | Unset
        if isinstance(_moderation, Unset):
            moderation = UNSET
        else:
            moderation = check_outbound_ai_response_policy_moderation(_moderation)

        outbound_ai_response_policy = cls(
            email=email,
            payment_card=payment_card,
            iban=iban,
            secret=secret,
            blocked_terms=blocked_terms,
            moderation=moderation,
        )

        outbound_ai_response_policy.additional_properties = d
        return outbound_ai_response_policy

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
