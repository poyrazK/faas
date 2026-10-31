import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.profile_response import ProfileResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    deployment_id: UUID,
    runtime: str,
    start: datetime.datetime,
    end: datetime.datetime,
    route: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_deployment_id = str(deployment_id)
    params["deployment_id"] = json_deployment_id

    params["runtime"] = runtime

    json_start = start.isoformat()
    params["start"] = json_start

    json_end = end.isoformat()
    params["end"] = json_end

    params["route"] = route

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/profiles".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ProfileResponse | None:
    if response.status_code == 200:
        response_200 = ProfileResponse.from_dict(response.json())

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
) -> Response[Problem | ProfileResponse]:
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
    deployment_id: UUID,
    runtime: str,
    start: datetime.datetime,
    end: datetime.datetime,
    route: str | Unset = UNSET,
) -> Response[Problem | ProfileResponse]:
    """Query sampled application CPU by deployment.

     Internal opt-in profiling. Returns function CPU totals and call paths; missing samples are
    explicitly empty. Times must fall within the plan retention window.

    Args:
        slug (str):
        deployment_id (UUID):
        runtime (str):
        start (datetime.datetime):
        end (datetime.datetime):
        route (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProfileResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment_id=deployment_id,
        runtime=runtime,
        start=start,
        end=end,
        route=route,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    deployment_id: UUID,
    runtime: str,
    start: datetime.datetime,
    end: datetime.datetime,
    route: str | Unset = UNSET,
) -> Problem | ProfileResponse | None:
    """Query sampled application CPU by deployment.

     Internal opt-in profiling. Returns function CPU totals and call paths; missing samples are
    explicitly empty. Times must fall within the plan retention window.

    Args:
        slug (str):
        deployment_id (UUID):
        runtime (str):
        start (datetime.datetime):
        end (datetime.datetime):
        route (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProfileResponse
    """

    return sync_detailed(
        slug=slug,
        client=client,
        deployment_id=deployment_id,
        runtime=runtime,
        start=start,
        end=end,
        route=route,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    deployment_id: UUID,
    runtime: str,
    start: datetime.datetime,
    end: datetime.datetime,
    route: str | Unset = UNSET,
) -> Response[Problem | ProfileResponse]:
    """Query sampled application CPU by deployment.

     Internal opt-in profiling. Returns function CPU totals and call paths; missing samples are
    explicitly empty. Times must fall within the plan retention window.

    Args:
        slug (str):
        deployment_id (UUID):
        runtime (str):
        start (datetime.datetime):
        end (datetime.datetime):
        route (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProfileResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment_id=deployment_id,
        runtime=runtime,
        start=start,
        end=end,
        route=route,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    deployment_id: UUID,
    runtime: str,
    start: datetime.datetime,
    end: datetime.datetime,
    route: str | Unset = UNSET,
) -> Problem | ProfileResponse | None:
    """Query sampled application CPU by deployment.

     Internal opt-in profiling. Returns function CPU totals and call paths; missing samples are
    explicitly empty. Times must fall within the plan retention window.

    Args:
        slug (str):
        deployment_id (UUID):
        runtime (str):
        start (datetime.datetime):
        end (datetime.datetime):
        route (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProfileResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            deployment_id=deployment_id,
            runtime=runtime,
            start=start,
            end=end,
            route=route,
        )
    ).parsed
