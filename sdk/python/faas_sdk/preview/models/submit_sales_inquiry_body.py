from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.submit_sales_inquiry_body_intent import (
    SubmitSalesInquiryBodyIntent,
    check_submit_sales_inquiry_body_intent,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="SubmitSalesInquiryBody")


@_attrs_define
class SubmitSalesInquiryBody:
    name: str
    email: str
    intent: SubmitSalesInquiryBodyIntent
    message: str
    """UTF-8 byte limit; other text limits are also measured in bytes."""
    challenge: str
    company: str | Unset = ""
    salesperson_code: str | Unset = ""
    website: str | Unset = ""
    """Leave blank."""

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        email = self.email

        intent: str = self.intent

        message = self.message

        challenge = self.challenge

        company = self.company

        salesperson_code = self.salesperson_code

        website = self.website

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "email": email,
                "intent": intent,
                "message": message,
                "challenge": challenge,
            }
        )
        if company is not UNSET:
            field_dict["company"] = company
        if salesperson_code is not UNSET:
            field_dict["salesperson_code"] = salesperson_code
        if website is not UNSET:
            field_dict["website"] = website

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        email = d.pop("email")

        intent = check_submit_sales_inquiry_body_intent(d.pop("intent"))

        message = d.pop("message")

        challenge = d.pop("challenge")

        company = d.pop("company", UNSET)

        salesperson_code = d.pop("salesperson_code", UNSET)

        website = d.pop("website", UNSET)

        submit_sales_inquiry_body = cls(
            name=name,
            email=email,
            intent=intent,
            message=message,
            challenge=challenge,
            company=company,
            salesperson_code=salesperson_code,
            website=website,
        )

        return submit_sales_inquiry_body
