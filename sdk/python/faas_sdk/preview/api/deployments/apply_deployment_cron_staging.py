from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.deployment_cron_staging_request import DeploymentCronStagingRequest
from ...models.deployment_staging_receipt import DeploymentStagingReceipt
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: DeploymentCronStagingRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/deployment-cron-staging".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DeploymentStagingReceipt | Problem | None:
    if response.status_code == 200:
        response_200 = DeploymentStagingReceipt.from_dict(response.json())

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
) -> Response[DeploymentStagingReceipt | Problem]:
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
    body: DeploymentCronStagingRequest,
) -> Response[DeploymentStagingReceipt | Problem]:
    """Prepare deferred cron activation or recover legacy cron staging.

     Prepare retains active schedules until the accepted production release is
    live and owns all traffic. Track_upload binds an owned upload for autonomous
    recovery of cached committed submissions. Cancel fences an unsubmitted intent.
    Repeating prepare reads its durable prepared, activated, conflict or discarded
    status. Legacy stage/restore replaces the complete set under locks. Command
    crons are preserved and count toward app and account quotas. The exact
    app, unchanged latest deployment, revision, owner and canonical intent
    guard staging and cleanup. Ordinary HTTP scheduling edits, including
    same-value updates, invalidate ownership; scheduler firing and suspension
    bookkeeping do not. Cleanup before a delayed stage fences it out. Accepted
    or uncertain deployment submission must retain staging. No builds are
    created; no unchecked fallback or HTTP response replay cache is used.
    Recovery restores scheduling configuration, not already dispatched work.

    Args:
        slug (str):
        body (DeploymentCronStagingRequest): Immutable HTTP cron intent. Prepare retains active
            schedules until the submitted production release owns all traffic; cancel fences an
            unsubmitted intent. Stage and restore support legacy recovery.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentStagingReceipt | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: DeploymentCronStagingRequest,
) -> DeploymentStagingReceipt | Problem | None:
    """Prepare deferred cron activation or recover legacy cron staging.

     Prepare retains active schedules until the accepted production release is
    live and owns all traffic. Track_upload binds an owned upload for autonomous
    recovery of cached committed submissions. Cancel fences an unsubmitted intent.
    Repeating prepare reads its durable prepared, activated, conflict or discarded
    status. Legacy stage/restore replaces the complete set under locks. Command
    crons are preserved and count toward app and account quotas. The exact
    app, unchanged latest deployment, revision, owner and canonical intent
    guard staging and cleanup. Ordinary HTTP scheduling edits, including
    same-value updates, invalidate ownership; scheduler firing and suspension
    bookkeeping do not. Cleanup before a delayed stage fences it out. Accepted
    or uncertain deployment submission must retain staging. No builds are
    created; no unchecked fallback or HTTP response replay cache is used.
    Recovery restores scheduling configuration, not already dispatched work.

    Args:
        slug (str):
        body (DeploymentCronStagingRequest): Immutable HTTP cron intent. Prepare retains active
            schedules until the submitted production release owns all traffic; cancel fences an
            unsubmitted intent. Stage and restore support legacy recovery.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentStagingReceipt | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: DeploymentCronStagingRequest,
) -> Response[DeploymentStagingReceipt | Problem]:
    """Prepare deferred cron activation or recover legacy cron staging.

     Prepare retains active schedules until the accepted production release is
    live and owns all traffic. Track_upload binds an owned upload for autonomous
    recovery of cached committed submissions. Cancel fences an unsubmitted intent.
    Repeating prepare reads its durable prepared, activated, conflict or discarded
    status. Legacy stage/restore replaces the complete set under locks. Command
    crons are preserved and count toward app and account quotas. The exact
    app, unchanged latest deployment, revision, owner and canonical intent
    guard staging and cleanup. Ordinary HTTP scheduling edits, including
    same-value updates, invalidate ownership; scheduler firing and suspension
    bookkeeping do not. Cleanup before a delayed stage fences it out. Accepted
    or uncertain deployment submission must retain staging. No builds are
    created; no unchecked fallback or HTTP response replay cache is used.
    Recovery restores scheduling configuration, not already dispatched work.

    Args:
        slug (str):
        body (DeploymentCronStagingRequest): Immutable HTTP cron intent. Prepare retains active
            schedules until the submitted production release owns all traffic; cancel fences an
            unsubmitted intent. Stage and restore support legacy recovery.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentStagingReceipt | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: DeploymentCronStagingRequest,
) -> DeploymentStagingReceipt | Problem | None:
    """Prepare deferred cron activation or recover legacy cron staging.

     Prepare retains active schedules until the accepted production release is
    live and owns all traffic. Track_upload binds an owned upload for autonomous
    recovery of cached committed submissions. Cancel fences an unsubmitted intent.
    Repeating prepare reads its durable prepared, activated, conflict or discarded
    status. Legacy stage/restore replaces the complete set under locks. Command
    crons are preserved and count toward app and account quotas. The exact
    app, unchanged latest deployment, revision, owner and canonical intent
    guard staging and cleanup. Ordinary HTTP scheduling edits, including
    same-value updates, invalidate ownership; scheduler firing and suspension
    bookkeeping do not. Cleanup before a delayed stage fences it out. Accepted
    or uncertain deployment submission must retain staging. No builds are
    created; no unchecked fallback or HTTP response replay cache is used.
    Recovery restores scheduling configuration, not already dispatched work.

    Args:
        slug (str):
        body (DeploymentCronStagingRequest): Immutable HTTP cron intent. Prepare retains active
            schedules until the submitted production release owns all traffic; cancel fences an
            unsubmitted intent. Stage and restore support legacy recovery.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentStagingReceipt | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
