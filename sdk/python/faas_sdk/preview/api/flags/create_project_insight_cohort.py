from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.insight_cohort import InsightCohort
from ...models.insight_cohort_request import InsightCohortRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    environment: str,
    *,
    body: InsightCohortRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/projects/{slug}/environments/{environment}/cohorts".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> InsightCohort | Problem | None:
    if response.status_code == 201:
        response_201 = InsightCohort.from_dict(response.json())

        return response_201

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

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
) -> Response[InsightCohort | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: InsightCohortRequest,
) -> Response[InsightCohort | Problem]:
    """Save a cohort.

     Members are computed within a minute, then hourly. At most 20 cohorts per environment (409
    analytics_cohort_limit); members per cohort are bounded by plan (Hobby 1,000, Pro 10,000, Scale
    100,000).

    Args:
        slug (str):
        environment (str):
        body (InsightCohortRequest): A cohort to save. did: customers that sent the event at least
            min_count times in the last within_days days; did_not: customers active in the last 30
            days that did not send it; funnel: customers that completed the steps within the window,
            starting in the last within_days days.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[InsightCohort | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: InsightCohortRequest,
) -> InsightCohort | Problem | None:
    """Save a cohort.

     Members are computed within a minute, then hourly. At most 20 cohorts per environment (409
    analytics_cohort_limit); members per cohort are bounded by plan (Hobby 1,000, Pro 10,000, Scale
    100,000).

    Args:
        slug (str):
        environment (str):
        body (InsightCohortRequest): A cohort to save. did: customers that sent the event at least
            min_count times in the last within_days days; did_not: customers active in the last 30
            days that did not send it; funnel: customers that completed the steps within the window,
            starting in the last within_days days.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        InsightCohort | Problem
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: InsightCohortRequest,
) -> Response[InsightCohort | Problem]:
    """Save a cohort.

     Members are computed within a minute, then hourly. At most 20 cohorts per environment (409
    analytics_cohort_limit); members per cohort are bounded by plan (Hobby 1,000, Pro 10,000, Scale
    100,000).

    Args:
        slug (str):
        environment (str):
        body (InsightCohortRequest): A cohort to save. did: customers that sent the event at least
            min_count times in the last within_days days; did_not: customers active in the last 30
            days that did not send it; funnel: customers that completed the steps within the window,
            starting in the last within_days days.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[InsightCohort | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: InsightCohortRequest,
) -> InsightCohort | Problem | None:
    """Save a cohort.

     Members are computed within a minute, then hourly. At most 20 cohorts per environment (409
    analytics_cohort_limit); members per cohort are bounded by plan (Hobby 1,000, Pro 10,000, Scale
    100,000).

    Args:
        slug (str):
        environment (str):
        body (InsightCohortRequest): A cohort to save. did: customers that sent the event at least
            min_count times in the last within_days days; did_not: customers active in the last 30
            days that did not send it; funnel: customers that completed the steps within the window,
            starting in the last within_days days.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        InsightCohort | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            client=client,
            body=body,
        )
    ).parsed
