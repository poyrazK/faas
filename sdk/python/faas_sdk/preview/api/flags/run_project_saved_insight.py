from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.run_project_saved_insight_format import (
    RunProjectSavedInsightFormat,
)
from ...models.saved_insight_result import SavedInsightResult
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    environment: str,
    key: str,
    *,
    format_: RunProjectSavedInsightFormat | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_format_: str | Unset = UNSET
    if not isinstance(format_, Unset):
        json_format_ = format_

    params["format"] = json_format_

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/projects/{slug}/environments/{environment}/insights/saved/{key}/run".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
            key=quote(str(key), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | SavedInsightResult | None:
    if response.status_code == 200:
        response_200 = SavedInsightResult.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

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
) -> Response[Problem | SavedInsightResult]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    environment: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    format_: RunProjectSavedInsightFormat | Unset = UNSET,
) -> Response[Problem | SavedInsightResult]:
    """Run a saved insight now.

     Read-only. Runs the saved query over its last range_days days, clamped to the plan's analytics
    retention, and returns its value and result. format=csv returns a CSV attachment instead: one row
    per series point, funnel step or retention period.

    Args:
        slug (str):
        environment (str):
        key (str):
        format_ (RunProjectSavedInsightFormat | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SavedInsightResult]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        key=key,
        format_=format_,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    environment: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    format_: RunProjectSavedInsightFormat | Unset = UNSET,
) -> Problem | SavedInsightResult | None:
    """Run a saved insight now.

     Read-only. Runs the saved query over its last range_days days, clamped to the plan's analytics
    retention, and returns its value and result. format=csv returns a CSV attachment instead: one row
    per series point, funnel step or retention period.

    Args:
        slug (str):
        environment (str):
        key (str):
        format_ (RunProjectSavedInsightFormat | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SavedInsightResult
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        key=key,
        client=client,
        format_=format_,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    format_: RunProjectSavedInsightFormat | Unset = UNSET,
) -> Response[Problem | SavedInsightResult]:
    """Run a saved insight now.

     Read-only. Runs the saved query over its last range_days days, clamped to the plan's analytics
    retention, and returns its value and result. format=csv returns a CSV attachment instead: one row
    per series point, funnel step or retention period.

    Args:
        slug (str):
        environment (str):
        key (str):
        format_ (RunProjectSavedInsightFormat | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SavedInsightResult]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        key=key,
        format_=format_,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    format_: RunProjectSavedInsightFormat | Unset = UNSET,
) -> Problem | SavedInsightResult | None:
    """Run a saved insight now.

     Read-only. Runs the saved query over its last range_days days, clamped to the plan's analytics
    retention, and returns its value and result. format=csv returns a CSV attachment instead: one row
    per series point, funnel step or retention period.

    Args:
        slug (str):
        environment (str):
        key (str):
        format_ (RunProjectSavedInsightFormat | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SavedInsightResult
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            key=key,
            client=client,
            format_=format_,
        )
    ).parsed
