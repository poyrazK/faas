from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.org_billing_email_request import OrgBillingEmailRequest
from ...models.org_billing_email_response import OrgBillingEmailResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: OrgBillingEmailRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/orgs/{slug}/billing/email".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OrgBillingEmailResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OrgBillingEmailResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 502:
        response_502 = Problem.from_dict(response.json())

        return response_502

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OrgBillingEmailResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: OrgBillingEmailRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[OrgBillingEmailResponse | Problem]:
    """Set the email the organization bills to.

     Requires the organization owner, admin credential scope, verified email,
    MFA and recent step-up. The billing provider sends this address the
    organization's invoices and signs it in to the organization's billing
    portal, so use an address the organization controls rather than a
    member's. The first organization checkout bills the member who starts
    it until this is set. When the organization already has a billing
    customer, the customer moves to the new email first and nothing changes
    if the provider refuses. The provider keeps one customer per email: an
    email another Gregale account or organization already pays with is
    refused with 409 `billing_email_in_use`. While a member's email is the
    billing email, that member cannot be removed (409
    `billing_contact_member`).

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (OrgBillingEmailRequest): The email an organization bills to; invoices and the
            billing portal sign-in go to it.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OrgBillingEmailResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: OrgBillingEmailRequest,
    idempotency_key: str | Unset = UNSET,
) -> OrgBillingEmailResponse | Problem | None:
    """Set the email the organization bills to.

     Requires the organization owner, admin credential scope, verified email,
    MFA and recent step-up. The billing provider sends this address the
    organization's invoices and signs it in to the organization's billing
    portal, so use an address the organization controls rather than a
    member's. The first organization checkout bills the member who starts
    it until this is set. When the organization already has a billing
    customer, the customer moves to the new email first and nothing changes
    if the provider refuses. The provider keeps one customer per email: an
    email another Gregale account or organization already pays with is
    refused with 409 `billing_email_in_use`. While a member's email is the
    billing email, that member cannot be removed (409
    `billing_contact_member`).

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (OrgBillingEmailRequest): The email an organization bills to; invoices and the
            billing portal sign-in go to it.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OrgBillingEmailResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: OrgBillingEmailRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[OrgBillingEmailResponse | Problem]:
    """Set the email the organization bills to.

     Requires the organization owner, admin credential scope, verified email,
    MFA and recent step-up. The billing provider sends this address the
    organization's invoices and signs it in to the organization's billing
    portal, so use an address the organization controls rather than a
    member's. The first organization checkout bills the member who starts
    it until this is set. When the organization already has a billing
    customer, the customer moves to the new email first and nothing changes
    if the provider refuses. The provider keeps one customer per email: an
    email another Gregale account or organization already pays with is
    refused with 409 `billing_email_in_use`. While a member's email is the
    billing email, that member cannot be removed (409
    `billing_contact_member`).

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (OrgBillingEmailRequest): The email an organization bills to; invoices and the
            billing portal sign-in go to it.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OrgBillingEmailResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: OrgBillingEmailRequest,
    idempotency_key: str | Unset = UNSET,
) -> OrgBillingEmailResponse | Problem | None:
    """Set the email the organization bills to.

     Requires the organization owner, admin credential scope, verified email,
    MFA and recent step-up. The billing provider sends this address the
    organization's invoices and signs it in to the organization's billing
    portal, so use an address the organization controls rather than a
    member's. The first organization checkout bills the member who starts
    it until this is set. When the organization already has a billing
    customer, the customer moves to the new email first and nothing changes
    if the provider refuses. The provider keeps one customer per email: an
    email another Gregale account or organization already pays with is
    refused with 409 `billing_email_in_use`. While a member's email is the
    billing email, that member cannot be removed (409
    `billing_contact_member`).

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (OrgBillingEmailRequest): The email an organization bills to; invoices and the
            billing portal sign-in go to it.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OrgBillingEmailResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
