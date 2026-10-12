import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.workflow_recovery_report import WorkflowRecoveryReport
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    workflow: str | Unset = UNSET,
    policy_version: int | Unset = UNSET,
    created_after: datetime.datetime | Unset = UNSET,
    created_before: datetime.datetime | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["workflow"] = workflow

    params["policy_version"] = policy_version

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
        "url": "/v1/apps/{slug}/recovery-policy/report".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | WorkflowRecoveryReport | None:
    if response.status_code == 200:
        response_200 = WorkflowRecoveryReport.from_dict(response.json())

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | WorkflowRecoveryReport]:
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
    workflow: str | Unset = UNSET,
    policy_version: int | Unset = UNSET,
    created_after: datetime.datetime | Unset = UNSET,
    created_before: datetime.datetime | Unset = UNSET,
) -> Response[Problem | WorkflowRecoveryReport]:
    """Report observed recovery automation effectiveness

     Read-only consistent snapshot summarizing at most 10000 newest retained history entries.
    Deduplicates durable and retained retry records by action. Attempts and results are selected
    independently by event time; missing results do not prove children are running. Durations measure
    application to result recording including worker polling delay. Groups use workflow and policy
    version. Blockers count distinct actions per observed reason. Recording is prospective, without
    backfill, and durable history retains 30 days / 10000 events per app. Coverage notes and truncation
    always accompany counts; no success percentage or causal effectiveness claim is inferred.

    Args:
        slug (str):
        workflow (str | Unset):
        policy_version (int | Unset):
        created_after (datetime.datetime | Unset):
        created_before (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowRecoveryReport]
    """

    kwargs = _get_kwargs(
        slug=slug,
        workflow=workflow,
        policy_version=policy_version,
        created_after=created_after,
        created_before=created_before,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
    workflow: str | Unset = UNSET,
    policy_version: int | Unset = UNSET,
    created_after: datetime.datetime | Unset = UNSET,
    created_before: datetime.datetime | Unset = UNSET,
) -> Problem | WorkflowRecoveryReport | None:
    """Report observed recovery automation effectiveness

     Read-only consistent snapshot summarizing at most 10000 newest retained history entries.
    Deduplicates durable and retained retry records by action. Attempts and results are selected
    independently by event time; missing results do not prove children are running. Durations measure
    application to result recording including worker polling delay. Groups use workflow and policy
    version. Blockers count distinct actions per observed reason. Recording is prospective, without
    backfill, and durable history retains 30 days / 10000 events per app. Coverage notes and truncation
    always accompany counts; no success percentage or causal effectiveness claim is inferred.

    Args:
        slug (str):
        workflow (str | Unset):
        policy_version (int | Unset):
        created_after (datetime.datetime | Unset):
        created_before (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowRecoveryReport
    """

    return sync_detailed(
        slug=slug,
        client=client,
        workflow=workflow,
        policy_version=policy_version,
        created_after=created_after,
        created_before=created_before,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    workflow: str | Unset = UNSET,
    policy_version: int | Unset = UNSET,
    created_after: datetime.datetime | Unset = UNSET,
    created_before: datetime.datetime | Unset = UNSET,
) -> Response[Problem | WorkflowRecoveryReport]:
    """Report observed recovery automation effectiveness

     Read-only consistent snapshot summarizing at most 10000 newest retained history entries.
    Deduplicates durable and retained retry records by action. Attempts and results are selected
    independently by event time; missing results do not prove children are running. Durations measure
    application to result recording including worker polling delay. Groups use workflow and policy
    version. Blockers count distinct actions per observed reason. Recording is prospective, without
    backfill, and durable history retains 30 days / 10000 events per app. Coverage notes and truncation
    always accompany counts; no success percentage or causal effectiveness claim is inferred.

    Args:
        slug (str):
        workflow (str | Unset):
        policy_version (int | Unset):
        created_after (datetime.datetime | Unset):
        created_before (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowRecoveryReport]
    """

    kwargs = _get_kwargs(
        slug=slug,
        workflow=workflow,
        policy_version=policy_version,
        created_after=created_after,
        created_before=created_before,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    workflow: str | Unset = UNSET,
    policy_version: int | Unset = UNSET,
    created_after: datetime.datetime | Unset = UNSET,
    created_before: datetime.datetime | Unset = UNSET,
) -> Problem | WorkflowRecoveryReport | None:
    """Report observed recovery automation effectiveness

     Read-only consistent snapshot summarizing at most 10000 newest retained history entries.
    Deduplicates durable and retained retry records by action. Attempts and results are selected
    independently by event time; missing results do not prove children are running. Durations measure
    application to result recording including worker polling delay. Groups use workflow and policy
    version. Blockers count distinct actions per observed reason. Recording is prospective, without
    backfill, and durable history retains 30 days / 10000 events per app. Coverage notes and truncation
    always accompany counts; no success percentage or causal effectiveness claim is inferred.

    Args:
        slug (str):
        workflow (str | Unset):
        policy_version (int | Unset):
        created_after (datetime.datetime | Unset):
        created_before (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowRecoveryReport
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            workflow=workflow,
            policy_version=policy_version,
            created_after=created_after,
            created_before=created_before,
        )
    ).parsed
