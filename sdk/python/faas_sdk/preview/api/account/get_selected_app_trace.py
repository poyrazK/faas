from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.selected_trace_lookup_response import SelectedTraceLookupResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    trace_id: str,
    *,
    app_id: list[UUID],
    limit: int | Unset = 10,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_app_id = []
    for app_id_item_data in app_id:
        app_id_item = str(app_id_item_data)
        json_app_id.append(app_id_item)

    params["app_id"] = json_app_id

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/account/traces/{trace_id}/selected-apps".format(
            trace_id=quote(str(trace_id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | SelectedTraceLookupResponse | None:
    if response.status_code == 200:
        response_200 = SelectedTraceLookupResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

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
) -> Response[Problem | SelectedTraceLookupResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    trace_id: str,
    *,
    client: AuthenticatedClient,
    app_id: list[UUID],
    limit: int | Unset = 10,
) -> Response[Problem | SelectedTraceLookupResponse]:
    """Look up trace metadata for explicitly selected app IDs.

     Requires one to five distinct canonical app IDs owned by the authenticated
    account. Both request and invocation SQL queries filter account and selected
    app IDs before ordering and limiting. No account-wide fallback, log reads,
    spans, payloads, arbitrary headers or queue names. Request metadata is
    oldest-first within plan telemetry retention; durable invocation lifecycle
    history has an independent oldest-first bound and no request-window filter.
    Returns per-source status and truncation, including empty or unavailable
    sources. No pagination, automatic follow-up or causal-edge inference.

    Args:
        trace_id (str):
        app_id (list[UUID]):
        limit (int | Unset):  Default: 10.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SelectedTraceLookupResponse]
    """

    kwargs = _get_kwargs(
        trace_id=trace_id,
        app_id=app_id,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    trace_id: str,
    *,
    client: AuthenticatedClient,
    app_id: list[UUID],
    limit: int | Unset = 10,
) -> Problem | SelectedTraceLookupResponse | None:
    """Look up trace metadata for explicitly selected app IDs.

     Requires one to five distinct canonical app IDs owned by the authenticated
    account. Both request and invocation SQL queries filter account and selected
    app IDs before ordering and limiting. No account-wide fallback, log reads,
    spans, payloads, arbitrary headers or queue names. Request metadata is
    oldest-first within plan telemetry retention; durable invocation lifecycle
    history has an independent oldest-first bound and no request-window filter.
    Returns per-source status and truncation, including empty or unavailable
    sources. No pagination, automatic follow-up or causal-edge inference.

    Args:
        trace_id (str):
        app_id (list[UUID]):
        limit (int | Unset):  Default: 10.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SelectedTraceLookupResponse
    """

    return sync_detailed(
        trace_id=trace_id,
        client=client,
        app_id=app_id,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    trace_id: str,
    *,
    client: AuthenticatedClient,
    app_id: list[UUID],
    limit: int | Unset = 10,
) -> Response[Problem | SelectedTraceLookupResponse]:
    """Look up trace metadata for explicitly selected app IDs.

     Requires one to five distinct canonical app IDs owned by the authenticated
    account. Both request and invocation SQL queries filter account and selected
    app IDs before ordering and limiting. No account-wide fallback, log reads,
    spans, payloads, arbitrary headers or queue names. Request metadata is
    oldest-first within plan telemetry retention; durable invocation lifecycle
    history has an independent oldest-first bound and no request-window filter.
    Returns per-source status and truncation, including empty or unavailable
    sources. No pagination, automatic follow-up or causal-edge inference.

    Args:
        trace_id (str):
        app_id (list[UUID]):
        limit (int | Unset):  Default: 10.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SelectedTraceLookupResponse]
    """

    kwargs = _get_kwargs(
        trace_id=trace_id,
        app_id=app_id,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    trace_id: str,
    *,
    client: AuthenticatedClient,
    app_id: list[UUID],
    limit: int | Unset = 10,
) -> Problem | SelectedTraceLookupResponse | None:
    """Look up trace metadata for explicitly selected app IDs.

     Requires one to five distinct canonical app IDs owned by the authenticated
    account. Both request and invocation SQL queries filter account and selected
    app IDs before ordering and limiting. No account-wide fallback, log reads,
    spans, payloads, arbitrary headers or queue names. Request metadata is
    oldest-first within plan telemetry retention; durable invocation lifecycle
    history has an independent oldest-first bound and no request-window filter.
    Returns per-source status and truncation, including empty or unavailable
    sources. No pagination, automatic follow-up or causal-edge inference.

    Args:
        trace_id (str):
        app_id (list[UUID]):
        limit (int | Unset):  Default: 10.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SelectedTraceLookupResponse
    """

    return (
        await asyncio_detailed(
            trace_id=trace_id,
            client=client,
            app_id=app_id,
            limit=limit,
        )
    ).parsed
