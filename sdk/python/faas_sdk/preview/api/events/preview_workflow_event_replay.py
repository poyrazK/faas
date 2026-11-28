import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.workflow_event_replay_preview_response import WorkflowEventReplayPreviewResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    workflow_name: str,
    from_: datetime.datetime,
    until: datetime.datetime,
    after: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["workflow_name"] = workflow_name

    json_from_ = from_.isoformat()
    params["from"] = json_from_

    json_until = until.isoformat()
    params["until"] = json_until

    params["after"] = after

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/workflow-event-replay-preview".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | WorkflowEventReplayPreviewResponse | None:
    if response.status_code == 200:
        response_200 = WorkflowEventReplayPreviewResponse.from_dict(response.json())

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
) -> Response[Problem | WorkflowEventReplayPreviewResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    workflow_name: str,
    from_: datetime.datetime,
    until: datetime.datetime,
    after: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> Response[Problem | WorkflowEventReplayPreviewResponse]:
    """Preview retained events for one captured workflow recipient.

     Read-only, bounded inspection of retained envelopes and their immutable
    workflow recipient snapshots. The preview evaluates the captured
    workflow trigger filter, reports its routing checkpoint and durable
    workflow admission receipt, and never creates a workflow run. It does
    not inspect events where the workflow was not captured, current workflow
    definitions, or current workflow quota/target eligibility. Potential
    admissions are estimates for a future replay policy, not a guarantee.
    Event payloads and captured definitions are never returned.

    Args:
        slug (str):
        workflow_name (str):
        from_ (datetime.datetime):
        until (datetime.datetime):
        after (str | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowEventReplayPreviewResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        workflow_name=workflow_name,
        from_=from_,
        until=until,
        after=after,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
    workflow_name: str,
    from_: datetime.datetime,
    until: datetime.datetime,
    after: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> Problem | WorkflowEventReplayPreviewResponse | None:
    """Preview retained events for one captured workflow recipient.

     Read-only, bounded inspection of retained envelopes and their immutable
    workflow recipient snapshots. The preview evaluates the captured
    workflow trigger filter, reports its routing checkpoint and durable
    workflow admission receipt, and never creates a workflow run. It does
    not inspect events where the workflow was not captured, current workflow
    definitions, or current workflow quota/target eligibility. Potential
    admissions are estimates for a future replay policy, not a guarantee.
    Event payloads and captured definitions are never returned.

    Args:
        slug (str):
        workflow_name (str):
        from_ (datetime.datetime):
        until (datetime.datetime):
        after (str | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowEventReplayPreviewResponse
    """

    return sync_detailed(
        slug=slug,
        client=client,
        workflow_name=workflow_name,
        from_=from_,
        until=until,
        after=after,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    workflow_name: str,
    from_: datetime.datetime,
    until: datetime.datetime,
    after: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> Response[Problem | WorkflowEventReplayPreviewResponse]:
    """Preview retained events for one captured workflow recipient.

     Read-only, bounded inspection of retained envelopes and their immutable
    workflow recipient snapshots. The preview evaluates the captured
    workflow trigger filter, reports its routing checkpoint and durable
    workflow admission receipt, and never creates a workflow run. It does
    not inspect events where the workflow was not captured, current workflow
    definitions, or current workflow quota/target eligibility. Potential
    admissions are estimates for a future replay policy, not a guarantee.
    Event payloads and captured definitions are never returned.

    Args:
        slug (str):
        workflow_name (str):
        from_ (datetime.datetime):
        until (datetime.datetime):
        after (str | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowEventReplayPreviewResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        workflow_name=workflow_name,
        from_=from_,
        until=until,
        after=after,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    workflow_name: str,
    from_: datetime.datetime,
    until: datetime.datetime,
    after: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> Problem | WorkflowEventReplayPreviewResponse | None:
    """Preview retained events for one captured workflow recipient.

     Read-only, bounded inspection of retained envelopes and their immutable
    workflow recipient snapshots. The preview evaluates the captured
    workflow trigger filter, reports its routing checkpoint and durable
    workflow admission receipt, and never creates a workflow run. It does
    not inspect events where the workflow was not captured, current workflow
    definitions, or current workflow quota/target eligibility. Potential
    admissions are estimates for a future replay policy, not a guarantee.
    Event payloads and captured definitions are never returned.

    Args:
        slug (str):
        workflow_name (str):
        from_ (datetime.datetime):
        until (datetime.datetime):
        after (str | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowEventReplayPreviewResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            workflow_name=workflow_name,
            from_=from_,
            until=until,
            after=after,
            limit=limit,
        )
    ).parsed
