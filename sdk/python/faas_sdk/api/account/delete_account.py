from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.account_deletion_response import AccountDeletionResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/account",
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AccountDeletionResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AccountDeletionResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AccountDeletionResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str | Unset = UNSET,
) -> Response[AccountDeletionResponse | Problem]:
    """Stage account deletion (30-day grace).

     Stages deletion. The account becomes `deleted_pending` for 30 days
    during which the customer can call `POST /v1/account/restore`. After
    the grace period, all rows are GC'd.

    Args:
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountDeletionResponse | Problem]
    """

    kwargs = _get_kwargs(
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str | Unset = UNSET,
) -> AccountDeletionResponse | Problem | None:
    """Stage account deletion (30-day grace).

     Stages deletion. The account becomes `deleted_pending` for 30 days
    during which the customer can call `POST /v1/account/restore`. After
    the grace period, all rows are GC'd.

    Args:
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountDeletionResponse | Problem
    """

    return sync_detailed(
        client=client,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str | Unset = UNSET,
) -> Response[AccountDeletionResponse | Problem]:
    """Stage account deletion (30-day grace).

     Stages deletion. The account becomes `deleted_pending` for 30 days
    during which the customer can call `POST /v1/account/restore`. After
    the grace period, all rows are GC'd.

    Args:
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountDeletionResponse | Problem]
    """

    kwargs = _get_kwargs(
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str | Unset = UNSET,
) -> AccountDeletionResponse | Problem | None:
    """Stage account deletion (30-day grace).

     Stages deletion. The account becomes `deleted_pending` for 30 days
    during which the customer can call `POST /v1/account/restore`. After
    the grace period, all rows are GC'd.

    Args:
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountDeletionResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            idempotency_key=idempotency_key,
        )
    ).parsed
