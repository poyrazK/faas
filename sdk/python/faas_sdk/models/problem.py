from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="Problem")


@_attrs_define
class Problem:
    """RFC 7807 problem+json envelope. The `code` field is the stable
    machine-readable identifier; clients branch on it. `limit` and
    `observed` are populated on quota errors. `docs_url` points the
    user at the next action. `billing_portal_url` is populated on
    `code: payment_required` so the dashboard can deep-link the
    customer to the Stripe-hosted billing portal (issue #142).
    `paddle_checkout_url` + `tx_id` are populated instead when the
    box is running on the Paddle billing provider
    (`FAAS_BILLING_PROVIDER=paddle`, ADR-025) — the customer lands
    on a Paddle-hosted checkout page for the target plan and the
    dashboard renders the transaction handle as a confirmation id.
    Exactly one of `billing_portal_url` or `paddle_checkout_url` is
    populated on a given 402 — never both.

        Example:
            {'type': 'https://docs.DOMAIN/errors/validation_failed', 'title': 'Validation failed', 'status': 422, 'code':
                'validation_failed', 'detail': 'ram_mb must be one of [128, 256, 512, 1024, 2048]', 'limit': None, 'observed':
                None, 'docs_url': 'https://docs.DOMAIN/errors/validation_failed'}

    """

    title: str
    status: int
    code: str
    """ Stable machine-readable error code. See StatusForCode in pkg/api/errors.go. """
    type_: str | Unset = UNSET
    detail: str | Unset = UNSET
    limit: int | None | Unset = UNSET
    observed: int | None | Unset = UNSET
    docs_url: str | Unset = UNSET
    billing_portal_url: str | Unset = UNSET
    paddle_checkout_url: str | Unset = UNSET
    """ Paddle-hosted checkout URL on a `payment_required` 402 when
    the box is running on the Paddle billing provider. Mutually
    exclusive with `billing_portal_url`.
     """
    tx_id: str | Unset = UNSET
    """ Paddle transaction handle (`txn_…`) on a `payment_required`
    402. Empty on the Stripe path. The dashboard renders this as
    a confirmation id after the customer completes checkout.
     """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        title = self.title

        status = self.status

        code = self.code

        type_ = self.type_

        detail = self.detail

        limit: int | None | Unset
        if isinstance(self.limit, Unset):
            limit = UNSET
        else:
            limit = self.limit

        observed: int | None | Unset
        if isinstance(self.observed, Unset):
            observed = UNSET
        else:
            observed = self.observed

        docs_url = self.docs_url

        billing_portal_url = self.billing_portal_url

        paddle_checkout_url = self.paddle_checkout_url

        tx_id = self.tx_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "title": title,
                "status": status,
                "code": code,
            }
        )
        if type_ is not UNSET:
            field_dict["type"] = type_
        if detail is not UNSET:
            field_dict["detail"] = detail
        if limit is not UNSET:
            field_dict["limit"] = limit
        if observed is not UNSET:
            field_dict["observed"] = observed
        if docs_url is not UNSET:
            field_dict["docs_url"] = docs_url
        if billing_portal_url is not UNSET:
            field_dict["billing_portal_url"] = billing_portal_url
        if paddle_checkout_url is not UNSET:
            field_dict["paddle_checkout_url"] = paddle_checkout_url
        if tx_id is not UNSET:
            field_dict["tx_id"] = tx_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        title = d.pop("title")

        status = d.pop("status")

        code = d.pop("code")

        type_ = d.pop("type", UNSET)

        detail = d.pop("detail", UNSET)

        def _parse_limit(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        limit = _parse_limit(d.pop("limit", UNSET))

        def _parse_observed(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        observed = _parse_observed(d.pop("observed", UNSET))

        docs_url = d.pop("docs_url", UNSET)

        billing_portal_url = d.pop("billing_portal_url", UNSET)

        paddle_checkout_url = d.pop("paddle_checkout_url", UNSET)

        tx_id = d.pop("tx_id", UNSET)

        problem = cls(
            title=title,
            status=status,
            code=code,
            type_=type_,
            detail=detail,
            limit=limit,
            observed=observed,
            docs_url=docs_url,
            billing_portal_url=billing_portal_url,
            paddle_checkout_url=paddle_checkout_url,
            tx_id=tx_id,
        )

        problem.additional_properties = d
        return problem

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
