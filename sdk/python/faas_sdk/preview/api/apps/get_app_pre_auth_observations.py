from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.get_app_pre_auth_observations_range import (
    GetAppPreAuthObservationsRange,
)
from ...models.pre_auth_observations_response import PreAuthObservationsResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    range_: GetAppPreAuthObservationsRange | Unset = "5m",
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_range_: str | Unset = UNSET
    if not isinstance(range_, Unset):
        json_range_ = range_

    params["range"] = json_range_

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/pre-auth-observations".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PreAuthObservationsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = PreAuthObservationsResponse.from_dict(response.json())

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
) -> Response[PreAuthObservationsResponse | Problem]:
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
    range_: GetAppPreAuthObservationsRange | Unset = "5m",
) -> Response[PreAuthObservationsResponse | Problem]:
    """Observe-mode decisions for each configured pre-auth policy.

     Read-only security telemetry, available on every app plan. Each policy
    reports how often observe mode would have blocked a request and the
    final response class of those requests. A 2xx response is a possible
    false-positive signal, not proof that the requester was legitimate.
    Route policy IDs are bounded slots (route_0..route_15 and
    failures_0..failures_15). Reordering or replacing routes within the
    requested range can mix counts from different configurations;
    use a window after the last policy edit. Empty counts may mean no
    traffic. On Prometheus failure,
    source starts with `degraded:` and counts are zero.

    Args:
        slug (str):
        range_ (GetAppPreAuthObservationsRange | Unset):  Default: '5m'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PreAuthObservationsResponse | Problem]
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
    range_: GetAppPreAuthObservationsRange | Unset = "5m",
) -> PreAuthObservationsResponse | Problem | None:
    """Observe-mode decisions for each configured pre-auth policy.

     Read-only security telemetry, available on every app plan. Each policy
    reports how often observe mode would have blocked a request and the
    final response class of those requests. A 2xx response is a possible
    false-positive signal, not proof that the requester was legitimate.
    Route policy IDs are bounded slots (route_0..route_15 and
    failures_0..failures_15). Reordering or replacing routes within the
    requested range can mix counts from different configurations;
    use a window after the last policy edit. Empty counts may mean no
    traffic. On Prometheus failure,
    source starts with `degraded:` and counts are zero.

    Args:
        slug (str):
        range_ (GetAppPreAuthObservationsRange | Unset):  Default: '5m'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PreAuthObservationsResponse | Problem
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
    range_: GetAppPreAuthObservationsRange | Unset = "5m",
) -> Response[PreAuthObservationsResponse | Problem]:
    """Observe-mode decisions for each configured pre-auth policy.

     Read-only security telemetry, available on every app plan. Each policy
    reports how often observe mode would have blocked a request and the
    final response class of those requests. A 2xx response is a possible
    false-positive signal, not proof that the requester was legitimate.
    Route policy IDs are bounded slots (route_0..route_15 and
    failures_0..failures_15). Reordering or replacing routes within the
    requested range can mix counts from different configurations;
    use a window after the last policy edit. Empty counts may mean no
    traffic. On Prometheus failure,
    source starts with `degraded:` and counts are zero.

    Args:
        slug (str):
        range_ (GetAppPreAuthObservationsRange | Unset):  Default: '5m'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PreAuthObservationsResponse | Problem]
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
    range_: GetAppPreAuthObservationsRange | Unset = "5m",
) -> PreAuthObservationsResponse | Problem | None:
    """Observe-mode decisions for each configured pre-auth policy.

     Read-only security telemetry, available on every app plan. Each policy
    reports how often observe mode would have blocked a request and the
    final response class of those requests. A 2xx response is a possible
    false-positive signal, not proof that the requester was legitimate.
    Route policy IDs are bounded slots (route_0..route_15 and
    failures_0..failures_15). Reordering or replacing routes within the
    requested range can mix counts from different configurations;
    use a window after the last policy edit. Empty counts may mean no
    traffic. On Prometheus failure,
    source starts with `degraded:` and counts are zero.

    Args:
        slug (str):
        range_ (GetAppPreAuthObservationsRange | Unset):  Default: '5m'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PreAuthObservationsResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            range_=range_,
        )
    ).parsed
