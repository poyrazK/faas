import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.automation_health_response import AutomationHealthResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    name: str,
    *,
    created_after: datetime.datetime | Unset = UNSET,
    created_before: datetime.datetime | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_created_after: str | Unset = UNSET
    if not isinstance(created_after, Unset):
        json_created_after = created_after.isoformat()
    params["created_after"] = json_created_after

    json_created_before: str | Unset = UNSET
    if not isinstance(created_before, Unset):
        json_created_before = created_before.isoformat()
    params["created_before"] = json_created_before

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/automations/{name}/health".format(
            slug=quote(str(slug), safe=""),
            name=quote(str(name), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AutomationHealthResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AutomationHealthResponse.from_dict(response.json())

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
) -> Response[AutomationHealthResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    created_after: datetime.datetime | Unset = UNSET,
    created_before: datetime.datetime | Unset = UNSET,
) -> Response[AutomationHealthResponse | Problem]:
    """Get bounded execution health for one automation.

     Returns run counts by status, the completed-run success rate, median
    and p95 duration, recent run identities and the most common failed
    steps. Inputs, outputs, and error text are never included. The default
    window is the previous seven days; the maximum window is 30 days.
    created_after and created_before are inclusive RFC3339 timestamps, and
    created_before may not be in the future. Failed loop items are grouped
    under their parent step; at most ten failed steps are returned.

    Args:
        slug (str):
        name (str):
        created_after (datetime.datetime | Unset):
        created_before (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AutomationHealthResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        created_after=created_after,
        created_before=created_before,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    created_after: datetime.datetime | Unset = UNSET,
    created_before: datetime.datetime | Unset = UNSET,
) -> AutomationHealthResponse | Problem | None:
    """Get bounded execution health for one automation.

     Returns run counts by status, the completed-run success rate, median
    and p95 duration, recent run identities and the most common failed
    steps. Inputs, outputs, and error text are never included. The default
    window is the previous seven days; the maximum window is 30 days.
    created_after and created_before are inclusive RFC3339 timestamps, and
    created_before may not be in the future. Failed loop items are grouped
    under their parent step; at most ten failed steps are returned.

    Args:
        slug (str):
        name (str):
        created_after (datetime.datetime | Unset):
        created_before (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AutomationHealthResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        name=name,
        client=client,
        created_after=created_after,
        created_before=created_before,
    ).parsed


async def asyncio_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    created_after: datetime.datetime | Unset = UNSET,
    created_before: datetime.datetime | Unset = UNSET,
) -> Response[AutomationHealthResponse | Problem]:
    """Get bounded execution health for one automation.

     Returns run counts by status, the completed-run success rate, median
    and p95 duration, recent run identities and the most common failed
    steps. Inputs, outputs, and error text are never included. The default
    window is the previous seven days; the maximum window is 30 days.
    created_after and created_before are inclusive RFC3339 timestamps, and
    created_before may not be in the future. Failed loop items are grouped
    under their parent step; at most ten failed steps are returned.

    Args:
        slug (str):
        name (str):
        created_after (datetime.datetime | Unset):
        created_before (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AutomationHealthResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        created_after=created_after,
        created_before=created_before,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    created_after: datetime.datetime | Unset = UNSET,
    created_before: datetime.datetime | Unset = UNSET,
) -> AutomationHealthResponse | Problem | None:
    """Get bounded execution health for one automation.

     Returns run counts by status, the completed-run success rate, median
    and p95 duration, recent run identities and the most common failed
    steps. Inputs, outputs, and error text are never included. The default
    window is the previous seven days; the maximum window is 30 days.
    created_after and created_before are inclusive RFC3339 timestamps, and
    created_before may not be in the future. Failed loop items are grouped
    under their parent step; at most ten failed steps are returned.

    Args:
        slug (str):
        name (str):
        created_after (datetime.datetime | Unset):
        created_before (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AutomationHealthResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            name=name,
            client=client,
            created_after=created_after,
            created_before=created_before,
        )
    ).parsed
