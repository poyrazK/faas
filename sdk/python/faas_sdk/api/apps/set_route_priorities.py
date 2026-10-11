from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.route_priorities_response import RoutePrioritiesResponse
from ...models.set_route_priorities_request import SetRoutePrioritiesRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: SetRoutePrioritiesRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/route-priorities".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RoutePrioritiesResponse | None:
    if response.status_code == 200:
        response_200 = RoutePrioritiesResponse.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RoutePrioritiesResponse]:
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
    body: SetRoutePrioritiesRequest,
) -> Response[Problem | RoutePrioritiesResponse]:
    r"""Replace an app's saved route priorities.

     Saves at most 20 rules, matched in order; the first match wins. A path
    is a route template (/users/{id}) or an edge-rule glob (/exports/*); an
    omitted method matches every method. An empty list saves \"no
    priorities\" and turns off the route-health default. Gateways apply the
    change within 30 seconds. Requires deploy write access and completed
    MFA. Body limit is 16 KiB.

    Args:
        slug (str):
        body (SetRoutePrioritiesRequest): Replaces the saved route priorities; an empty list saves
            none.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RoutePrioritiesResponse]
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
    body: SetRoutePrioritiesRequest,
) -> Problem | RoutePrioritiesResponse | None:
    r"""Replace an app's saved route priorities.

     Saves at most 20 rules, matched in order; the first match wins. A path
    is a route template (/users/{id}) or an edge-rule glob (/exports/*); an
    omitted method matches every method. An empty list saves \"no
    priorities\" and turns off the route-health default. Gateways apply the
    change within 30 seconds. Requires deploy write access and completed
    MFA. Body limit is 16 KiB.

    Args:
        slug (str):
        body (SetRoutePrioritiesRequest): Replaces the saved route priorities; an empty list saves
            none.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RoutePrioritiesResponse
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
    body: SetRoutePrioritiesRequest,
) -> Response[Problem | RoutePrioritiesResponse]:
    r"""Replace an app's saved route priorities.

     Saves at most 20 rules, matched in order; the first match wins. A path
    is a route template (/users/{id}) or an edge-rule glob (/exports/*); an
    omitted method matches every method. An empty list saves \"no
    priorities\" and turns off the route-health default. Gateways apply the
    change within 30 seconds. Requires deploy write access and completed
    MFA. Body limit is 16 KiB.

    Args:
        slug (str):
        body (SetRoutePrioritiesRequest): Replaces the saved route priorities; an empty list saves
            none.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RoutePrioritiesResponse]
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
    body: SetRoutePrioritiesRequest,
) -> Problem | RoutePrioritiesResponse | None:
    r"""Replace an app's saved route priorities.

     Saves at most 20 rules, matched in order; the first match wins. A path
    is a route template (/users/{id}) or an edge-rule glob (/exports/*); an
    omitted method matches every method. An empty list saves \"no
    priorities\" and turns off the route-health default. Gateways apply the
    change within 30 seconds. Requires deploy write access and completed
    MFA. Body limit is 16 KiB.

    Args:
        slug (str):
        body (SetRoutePrioritiesRequest): Replaces the saved route priorities; an empty list saves
            none.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RoutePrioritiesResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
