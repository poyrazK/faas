from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.alert_delivery_response import AlertDeliveryResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    id: str,
    *,
    include_test: bool | Unset = False,
    limit: int | Unset = 50,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["include_test"] = include_test

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/alerts/{id}/deliveries".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | list[AlertDeliveryResponse] | None:
    if response.status_code == 200:
        response_200 = []
        _response_200 = response.json()
        for response_200_item_data in _response_200:
            response_200_item = AlertDeliveryResponse.from_dict(response_200_item_data)

            response_200.append(response_200_item)

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | list[AlertDeliveryResponse]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    include_test: bool | Unset = False,
    limit: int | Unset = 50,
) -> Response[Problem | list[AlertDeliveryResponse]]:
    """List recent alert_deliveries rows for one rule.

     Returns the most-recent alert_deliveries rows for the rule,
    newest-first. The default (include_test=false) hides test
    rows; the operator pane is reachable via ?include_test=true.
    IDOR-safe: a 404 is returned when the rule is on another
    account (same posture as GET /v1/apps/{slug}/alerts/{id}).

    Args:
        slug (str):
        id (str):
        include_test (bool | Unset):  Default: False.
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | list[AlertDeliveryResponse]]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        include_test=include_test,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    include_test: bool | Unset = False,
    limit: int | Unset = 50,
) -> Problem | list[AlertDeliveryResponse] | None:
    """List recent alert_deliveries rows for one rule.

     Returns the most-recent alert_deliveries rows for the rule,
    newest-first. The default (include_test=false) hides test
    rows; the operator pane is reachable via ?include_test=true.
    IDOR-safe: a 404 is returned when the rule is on another
    account (same posture as GET /v1/apps/{slug}/alerts/{id}).

    Args:
        slug (str):
        id (str):
        include_test (bool | Unset):  Default: False.
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | list[AlertDeliveryResponse]
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        include_test=include_test,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    include_test: bool | Unset = False,
    limit: int | Unset = 50,
) -> Response[Problem | list[AlertDeliveryResponse]]:
    """List recent alert_deliveries rows for one rule.

     Returns the most-recent alert_deliveries rows for the rule,
    newest-first. The default (include_test=false) hides test
    rows; the operator pane is reachable via ?include_test=true.
    IDOR-safe: a 404 is returned when the rule is on another
    account (same posture as GET /v1/apps/{slug}/alerts/{id}).

    Args:
        slug (str):
        id (str):
        include_test (bool | Unset):  Default: False.
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | list[AlertDeliveryResponse]]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        include_test=include_test,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    include_test: bool | Unset = False,
    limit: int | Unset = 50,
) -> Problem | list[AlertDeliveryResponse] | None:
    """List recent alert_deliveries rows for one rule.

     Returns the most-recent alert_deliveries rows for the rule,
    newest-first. The default (include_test=false) hides test
    rows; the operator pane is reachable via ?include_test=true.
    IDOR-safe: a 404 is returned when the rule is on another
    account (same posture as GET /v1/apps/{slug}/alerts/{id}).

    Args:
        slug (str):
        id (str):
        include_test (bool | Unset):  Default: False.
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | list[AlertDeliveryResponse]
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            include_test=include_test,
            limit=limit,
        )
    ).parsed
