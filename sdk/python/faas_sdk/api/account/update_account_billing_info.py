from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.account_response import AccountResponse
from ...models.problem import Problem
from ...models.update_account_billing_info_request import UpdateAccountBillingInfoRequest
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    body: UpdateAccountBillingInfoRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/v1/account/billing",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AccountResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AccountResponse.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AccountResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: UpdateAccountBillingInfoRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[AccountResponse | Problem]:
    """Update the account's invoice billing identity.

     Updates the legal business name, billing address, and tax identifier
    used for future provider-issued invoices. Omitted fields are kept;
    an explicitly empty string clears a field. The audit event records
    only changed field names, never the submitted values.

    Args:
        idempotency_key (str | Unset):
        body (UpdateAccountBillingInfoRequest): Partial legal billing identity update. Empty
            strings clear fields.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: UpdateAccountBillingInfoRequest,
    idempotency_key: str | Unset = UNSET,
) -> AccountResponse | Problem | None:
    """Update the account's invoice billing identity.

     Updates the legal business name, billing address, and tax identifier
    used for future provider-issued invoices. Omitted fields are kept;
    an explicitly empty string clears a field. The audit event records
    only changed field names, never the submitted values.

    Args:
        idempotency_key (str | Unset):
        body (UpdateAccountBillingInfoRequest): Partial legal billing identity update. Empty
            strings clear fields.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: UpdateAccountBillingInfoRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[AccountResponse | Problem]:
    """Update the account's invoice billing identity.

     Updates the legal business name, billing address, and tax identifier
    used for future provider-issued invoices. Omitted fields are kept;
    an explicitly empty string clears a field. The audit event records
    only changed field names, never the submitted values.

    Args:
        idempotency_key (str | Unset):
        body (UpdateAccountBillingInfoRequest): Partial legal billing identity update. Empty
            strings clear fields.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: UpdateAccountBillingInfoRequest,
    idempotency_key: str | Unset = UNSET,
) -> AccountResponse | Problem | None:
    """Update the account's invoice billing identity.

     Updates the legal business name, billing address, and tax identifier
    used for future provider-issued invoices. Omitted fields are kept;
    an explicitly empty string clears a field. The audit event records
    only changed field names, never the submitted values.

    Args:
        idempotency_key (str | Unset):
        body (UpdateAccountBillingInfoRequest): Partial legal billing identity update. Empty
            strings clear fields.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
