import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.flag_experiment_holdout_report import FlagExperimentHoldoutReport
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    environment: str,
    *,
    event: str,
    from_: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["event"] = event

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
        "url": "/v1/projects/{slug}/environments/{environment}/experiment-holdout/report".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> FlagExperimentHoldoutReport | Problem | None:
    if response.status_code == 200:
        response_200 = FlagExperimentHoldoutReport.from_dict(response.json())

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
) -> Response[FlagExperimentHoldoutReport | Problem]:
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
    from_: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> Response[FlagExperimentHoldoutReport | Problem]:
    """Compare held-out customers with everyone else.

     The cumulative effect of experimentation: the share of held-out and other active customers that sent
    the event in the range (default the last 30 days). Requires product analytics.

    Args:
        slug (str):
        environment (str):
        event (str):
        from_ (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FlagExperimentHoldoutReport | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        event=event,
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
    from_: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> FlagExperimentHoldoutReport | Problem | None:
    """Compare held-out customers with everyone else.

     The cumulative effect of experimentation: the share of held-out and other active customers that sent
    the event in the range (default the last 30 days). Requires product analytics.

    Args:
        slug (str):
        environment (str):
        event (str):
        from_ (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FlagExperimentHoldoutReport | Problem
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        client=client,
        event=event,
        from_=from_,
        until=until,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    event: str,
    from_: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> Response[FlagExperimentHoldoutReport | Problem]:
    """Compare held-out customers with everyone else.

     The cumulative effect of experimentation: the share of held-out and other active customers that sent
    the event in the range (default the last 30 days). Requires product analytics.

    Args:
        slug (str):
        environment (str):
        event (str):
        from_ (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FlagExperimentHoldoutReport | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        event=event,
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
    from_: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> FlagExperimentHoldoutReport | Problem | None:
    """Compare held-out customers with everyone else.

     The cumulative effect of experimentation: the share of held-out and other active customers that sent
    the event in the range (default the last 30 days). Requires product analytics.

    Args:
        slug (str):
        environment (str):
        event (str):
        from_ (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FlagExperimentHoldoutReport | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            client=client,
            event=event,
            from_=from_,
            until=until,
        )
    ).parsed
