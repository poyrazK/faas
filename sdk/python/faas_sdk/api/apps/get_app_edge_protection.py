from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.edge_protection_response import EdgeProtectionResponse
from ...models.get_app_edge_protection_range import GetAppEdgeProtectionRange
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    range_: GetAppEdgeProtectionRange | Unset = "1h",
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_range_: str | Unset = UNSET
    if not isinstance(range_, Unset):
        json_range_ = range_

    params["range"] = json_range_

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/edge-protection".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EdgeProtectionResponse | Problem | None:
    if response.status_code == 200:
        response_200 = EdgeProtectionResponse.from_dict(response.json())

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EdgeProtectionResponse | Problem]:
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
    range_: GetAppEdgeProtectionRange | Unset = "1h",
) -> Response[EdgeProtectionResponse | Problem]:
    """Summarize what the edge rejected for an app.

     Read-only security telemetry from the per-app gateway counters that
    the edge security alert presets evaluate: pre-auth source-limit
    decisions, kind=validate mismatches by mode, and requests answered by
    the other edge gates (JWT, IP, geo, ingress, body limits, throttles)
    by gate and status. On Prometheus failure, source starts with
    `degraded:` and counts are zero.

    Args:
        slug (str):
        range_ (GetAppEdgeProtectionRange | Unset):  Default: '1h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EdgeProtectionResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        range_=range_,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    range_: GetAppEdgeProtectionRange | Unset = "1h",
) -> EdgeProtectionResponse | Problem | None:
    """Summarize what the edge rejected for an app.

     Read-only security telemetry from the per-app gateway counters that
    the edge security alert presets evaluate: pre-auth source-limit
    decisions, kind=validate mismatches by mode, and requests answered by
    the other edge gates (JWT, IP, geo, ingress, body limits, throttles)
    by gate and status. On Prometheus failure, source starts with
    `degraded:` and counts are zero.

    Args:
        slug (str):
        range_ (GetAppEdgeProtectionRange | Unset):  Default: '1h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EdgeProtectionResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        range_=range_,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    range_: GetAppEdgeProtectionRange | Unset = "1h",
) -> Response[EdgeProtectionResponse | Problem]:
    """Summarize what the edge rejected for an app.

     Read-only security telemetry from the per-app gateway counters that
    the edge security alert presets evaluate: pre-auth source-limit
    decisions, kind=validate mismatches by mode, and requests answered by
    the other edge gates (JWT, IP, geo, ingress, body limits, throttles)
    by gate and status. On Prometheus failure, source starts with
    `degraded:` and counts are zero.

    Args:
        slug (str):
        range_ (GetAppEdgeProtectionRange | Unset):  Default: '1h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EdgeProtectionResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        range_=range_,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    range_: GetAppEdgeProtectionRange | Unset = "1h",
) -> EdgeProtectionResponse | Problem | None:
    """Summarize what the edge rejected for an app.

     Read-only security telemetry from the per-app gateway counters that
    the edge security alert presets evaluate: pre-auth source-limit
    decisions, kind=validate mismatches by mode, and requests answered by
    the other edge gates (JWT, IP, geo, ingress, body limits, throttles)
    by gate and status. On Prometheus failure, source starts with
    `degraded:` and counts are zero.

    Args:
        slug (str):
        range_ (GetAppEdgeProtectionRange | Unset):  Default: '1h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EdgeProtectionResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            range_=range_,
        )
    ).parsed
