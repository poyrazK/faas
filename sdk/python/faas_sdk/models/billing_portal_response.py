from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.payment_method_summary import PaymentMethodSummary


T = TypeVar("T", bound="BillingPortalResponse")


@_attrs_define
class BillingPortalResponse:
    """Provider-authenticated or operator-configured billing portal URL (issue #253) plus the
    card-on-file summary (issue #242). The url field is omitted
    when neither a provider session nor FAAS_BILLING_PORTAL_URL is
    available — that is the "absent" sentinel; the CLI branches on it
    to print a friendly hint instead of opening the browser to "". The payment_method
    field is omitted when the account has no card on file (Free
    plan, or post-checkout before the first paid cycle settles).

    """

    url: None | str | Unset = UNSET
    """Short-lived provider session URL, or substituted operator URL when no provider session is available."""
    payment_method: PaymentMethodSummary | Unset = UNSET
    """Card-on-file summary (issue #242). Provider-agnostic shape —
    the same wire shape is returned whether the operator runs
    Stripe or Paddle. brand is the lowercase network label
    ("visa", "mastercard", "amex"); last4 is the last-4 of the
    PAN (no full PAN, no PCI surface); exp_month / exp_year are
    integer card-face expiry fields.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        url: None | str | Unset
        if isinstance(self.url, Unset):
            url = UNSET
        else:
            url = self.url

        payment_method: dict[str, Any] | Unset = UNSET
        if not isinstance(self.payment_method, Unset):
            payment_method = self.payment_method.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if url is not UNSET:
            field_dict["url"] = url
        if payment_method is not UNSET:
            field_dict["payment_method"] = payment_method

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.payment_method_summary import PaymentMethodSummary

        d = dict(src_dict)

        def _parse_url(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        url = _parse_url(d.pop("url", UNSET))

        _payment_method = d.pop("payment_method", UNSET)
        payment_method: PaymentMethodSummary | Unset
        if isinstance(_payment_method, Unset):
            payment_method = UNSET
        else:
            payment_method = PaymentMethodSummary.from_dict(_payment_method)

        billing_portal_response = cls(
            url=url,
            payment_method=payment_method,
        )

        billing_portal_response.additional_properties = d
        return billing_portal_response

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
