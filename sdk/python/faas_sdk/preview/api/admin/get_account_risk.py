from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.account_risk_response import AccountRiskResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: UUID,
    *,
    include_pii: bool | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["include_pii"] = include_pii

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/admin/accounts/{id}/risk".format(
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AccountRiskResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AccountRiskResponse.from_dict(response.json())

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AccountRiskResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    include_pii: bool | Unset = UNSET,
) -> Response[AccountRiskResponse | Problem]:
    """An account's abuse risk (operator-only).

     The account's abuse hold, sign-up record (country, network owner,
    email domain, disposable flag), links to and from abuse-held
    accounts, abuse strikes inside the hold window, and abuse events
    (holds, CPU signals, abuse scans, abuse recycles) of the last 30
    days, newest first.

    Args:
        id (UUID):
        include_pii (bool | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountRiskResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        include_pii=include_pii,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    include_pii: bool | Unset = UNSET,
) -> AccountRiskResponse | Problem | None:
    """An account's abuse risk (operator-only).

     The account's abuse hold, sign-up record (country, network owner,
    email domain, disposable flag), links to and from abuse-held
    accounts, abuse strikes inside the hold window, and abuse events
    (holds, CPU signals, abuse scans, abuse recycles) of the last 30
    days, newest first.

    Args:
        id (UUID):
        include_pii (bool | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountRiskResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        include_pii=include_pii,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    include_pii: bool | Unset = UNSET,
) -> Response[AccountRiskResponse | Problem]:
    """An account's abuse risk (operator-only).

     The account's abuse hold, sign-up record (country, network owner,
    email domain, disposable flag), links to and from abuse-held
    accounts, abuse strikes inside the hold window, and abuse events
    (holds, CPU signals, abuse scans, abuse recycles) of the last 30
    days, newest first.

    Args:
        id (UUID):
        include_pii (bool | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountRiskResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        include_pii=include_pii,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    include_pii: bool | Unset = UNSET,
) -> AccountRiskResponse | Problem | None:
    """An account's abuse risk (operator-only).

     The account's abuse hold, sign-up record (country, network owner,
    email domain, disposable flag), links to and from abuse-held
    accounts, abuse strikes inside the hold window, and abuse events
    (holds, CPU signals, abuse scans, abuse recycles) of the last 30
    days, newest first.

    Args:
        id (UUID):
        include_pii (bool | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountRiskResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            include_pii=include_pii,
        )
    ).parsed
