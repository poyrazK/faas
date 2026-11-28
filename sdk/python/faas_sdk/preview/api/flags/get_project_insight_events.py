import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.get_project_insight_events_interval import (
    GetProjectInsightEventsInterval,
)
from ...models.get_project_insight_events_unit import GetProjectInsightEventsUnit
from ...models.insight_event_series import InsightEventSeries
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    environment: str,
    *,
    event: str,
    interval: GetProjectInsightEventsInterval | Unset = "day",
    unit: GetProjectInsightEventsUnit | Unset = "customer",
    by: str | Unset = UNSET,
    where: list[str] | Unset = UNSET,
    from_: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["event"] = event

    json_interval: str | Unset = UNSET
    if not isinstance(interval, Unset):
        json_interval = interval

    params["interval"] = json_interval

    json_unit: str | Unset = UNSET
    if not isinstance(unit, Unset):
        json_unit = unit

    params["unit"] = json_unit

    params["by"] = by

    json_where: list[str] | Unset = UNSET
    if not isinstance(where, Unset):
        json_where = where

    params["where"] = json_where

    json_from_: str | Unset = UNSET
    if not isinstance(from_, Unset):
        json_from_ = from_.isoformat()
    params["from"] = json_from_

    json_until: str | Unset = UNSET
    if not isinstance(until, Unset):
        json_until = until.isoformat()
    params["until"] = json_until

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/projects/{slug}/environments/{environment}/insights/events".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> InsightEventSeries | Problem | None:
    if response.status_code == 200:
        response_200 = InsightEventSeries.from_dict(response.json())

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
) -> Response[InsightEventSeries | Problem]:
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
    event: str,
    interval: GetProjectInsightEventsInterval | Unset = "day",
    unit: GetProjectInsightEventsUnit | Unset = "customer",
    by: str | Unset = UNSET,
    where: list[str] | Unset = UNSET,
    from_: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> Response[InsightEventSeries | Problem]:
    """Read a product analytics event series.

     Counts, distinct units and summed value of one track() event per time bucket, optionally filtered by
    properties and broken down by a flag's served value or a property. Requires the product_analytics
    platform flag and a plan with analytics retention. The range defaults to the last 7 days and is
    bounded by the plan's analytics retention and AnalyticsQueryMaxDays.

    Args:
        slug (str):
        environment (str):
        event (str):
        interval (GetProjectInsightEventsInterval | Unset):  Default: 'day'.
        unit (GetProjectInsightEventsUnit | Unset):  Default: 'customer'.
        by (str | Unset):
        where (list[str] | Unset):
        from_ (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[InsightEventSeries | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        event=event,
        interval=interval,
        unit=unit,
        by=by,
        where=where,
        from_=from_,
        until=until,
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
    event: str,
    interval: GetProjectInsightEventsInterval | Unset = "day",
    unit: GetProjectInsightEventsUnit | Unset = "customer",
    by: str | Unset = UNSET,
    where: list[str] | Unset = UNSET,
    from_: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> InsightEventSeries | Problem | None:
    """Read a product analytics event series.

     Counts, distinct units and summed value of one track() event per time bucket, optionally filtered by
    properties and broken down by a flag's served value or a property. Requires the product_analytics
    platform flag and a plan with analytics retention. The range defaults to the last 7 days and is
    bounded by the plan's analytics retention and AnalyticsQueryMaxDays.

    Args:
        slug (str):
        environment (str):
        event (str):
        interval (GetProjectInsightEventsInterval | Unset):  Default: 'day'.
        unit (GetProjectInsightEventsUnit | Unset):  Default: 'customer'.
        by (str | Unset):
        where (list[str] | Unset):
        from_ (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        InsightEventSeries | Problem
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        client=client,
        event=event,
        interval=interval,
        unit=unit,
        by=by,
        where=where,
        from_=from_,
        until=until,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    event: str,
    interval: GetProjectInsightEventsInterval | Unset = "day",
    unit: GetProjectInsightEventsUnit | Unset = "customer",
    by: str | Unset = UNSET,
    where: list[str] | Unset = UNSET,
    from_: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> Response[InsightEventSeries | Problem]:
    """Read a product analytics event series.

     Counts, distinct units and summed value of one track() event per time bucket, optionally filtered by
    properties and broken down by a flag's served value or a property. Requires the product_analytics
    platform flag and a plan with analytics retention. The range defaults to the last 7 days and is
    bounded by the plan's analytics retention and AnalyticsQueryMaxDays.

    Args:
        slug (str):
        environment (str):
        event (str):
        interval (GetProjectInsightEventsInterval | Unset):  Default: 'day'.
        unit (GetProjectInsightEventsUnit | Unset):  Default: 'customer'.
        by (str | Unset):
        where (list[str] | Unset):
        from_ (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[InsightEventSeries | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        event=event,
        interval=interval,
        unit=unit,
        by=by,
        where=where,
        from_=from_,
        until=until,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    event: str,
    interval: GetProjectInsightEventsInterval | Unset = "day",
    unit: GetProjectInsightEventsUnit | Unset = "customer",
    by: str | Unset = UNSET,
    where: list[str] | Unset = UNSET,
    from_: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> InsightEventSeries | Problem | None:
    """Read a product analytics event series.

     Counts, distinct units and summed value of one track() event per time bucket, optionally filtered by
    properties and broken down by a flag's served value or a property. Requires the product_analytics
    platform flag and a plan with analytics retention. The range defaults to the last 7 days and is
    bounded by the plan's analytics retention and AnalyticsQueryMaxDays.

    Args:
        slug (str):
        environment (str):
        event (str):
        interval (GetProjectInsightEventsInterval | Unset):  Default: 'day'.
        unit (GetProjectInsightEventsUnit | Unset):  Default: 'customer'.
        by (str | Unset):
        where (list[str] | Unset):
        from_ (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        InsightEventSeries | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            client=client,
            event=event,
            interval=interval,
            unit=unit,
            by=by,
            where=where,
            from_=from_,
            until=until,
        )
    ).parsed
