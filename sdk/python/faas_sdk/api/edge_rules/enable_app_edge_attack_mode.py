from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.edge_attack_mode_status import EdgeAttackModeStatus
from ...models.enable_edge_attack_mode_request import EnableEdgeAttackModeRequest
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: EnableEdgeAttackModeRequest | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/edge-protection/attack-mode".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    if not isinstance(body, Unset):
        _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EdgeAttackModeStatus | Problem | None:
    if response.status_code == 200:
        response_200 = EdgeAttackModeStatus.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

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
) -> Response[EdgeAttackModeStatus | Problem]:
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
    body: EnableEdgeAttackModeRequest | Unset = UNSET,
) -> Response[EdgeAttackModeStatus | Problem]:
    """Switch attack mode on, or extend it.

     Challenges every request to every host of the app until it ends. Available on every plan, outside
    the plan's
    edge-rule quotas. The response returns once the serving gateways
    have applied it; `X-Faas-Edge-Rules-State: converging` with 503 means it
    was saved but not yet acknowledged. An empty body applies the
    default duration of one hour.

    Args:
        slug (str):
        body (EnableEdgeAttackModeRequest | Unset): Switches attack mode on for a duration, or
            moves the end of an active one.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EdgeAttackModeStatus | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: EnableEdgeAttackModeRequest | Unset = UNSET,
) -> EdgeAttackModeStatus | Problem | None:
    """Switch attack mode on, or extend it.

     Challenges every request to every host of the app until it ends. Available on every plan, outside
    the plan's
    edge-rule quotas. The response returns once the serving gateways
    have applied it; `X-Faas-Edge-Rules-State: converging` with 503 means it
    was saved but not yet acknowledged. An empty body applies the
    default duration of one hour.

    Args:
        slug (str):
        body (EnableEdgeAttackModeRequest | Unset): Switches attack mode on for a duration, or
            moves the end of an active one.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EdgeAttackModeStatus | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: EnableEdgeAttackModeRequest | Unset = UNSET,
) -> Response[EdgeAttackModeStatus | Problem]:
    """Switch attack mode on, or extend it.

     Challenges every request to every host of the app until it ends. Available on every plan, outside
    the plan's
    edge-rule quotas. The response returns once the serving gateways
    have applied it; `X-Faas-Edge-Rules-State: converging` with 503 means it
    was saved but not yet acknowledged. An empty body applies the
    default duration of one hour.

    Args:
        slug (str):
        body (EnableEdgeAttackModeRequest | Unset): Switches attack mode on for a duration, or
            moves the end of an active one.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EdgeAttackModeStatus | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: EnableEdgeAttackModeRequest | Unset = UNSET,
) -> EdgeAttackModeStatus | Problem | None:
    """Switch attack mode on, or extend it.

     Challenges every request to every host of the app until it ends. Available on every plan, outside
    the plan's
    edge-rule quotas. The response returns once the serving gateways
    have applied it; `X-Faas-Edge-Rules-State: converging` with 503 means it
    was saved but not yet acknowledged. An empty body applies the
    default duration of one hour.

    Args:
        slug (str):
        body (EnableEdgeAttackModeRequest | Unset): Switches attack mode on for a duration, or
            moves the end of an active one.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EdgeAttackModeStatus | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
