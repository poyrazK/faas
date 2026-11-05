from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.object_bucket_notifications import ObjectBucketNotifications
from ...models.problem import Problem
from ...models.reconcile_object_storage_compute_triggers_request import ReconcileObjectStorageComputeTriggersRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    bucket: UUID,
    *,
    body: ReconcileObjectStorageComputeTriggersRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/buckets/{bucket}/compute-triggers".format(
            slug=quote(str(slug), safe=""),
            bucket=quote(str(bucket), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ObjectBucketNotifications | Problem:
    if response.status_code == 200:
        response_200 = ObjectBucketNotifications.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObjectBucketNotifications | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    bucket: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ReconcileObjectStorageComputeTriggersRequest,
) -> Response[ObjectBucketNotifications | Problem]:
    """Reconcile managed ObjectCreated triggers for the bucket's owning app

     Requires MFA, storage manage scope and bucket write access. The destination
    is always this bucket's owning app in its region. expected_revision is
    mandatory and stale revisions return 409, including unchanged requests.
    Replaces only managed rules, preserving manual notification rules.
    Explicit triggers: [] clears managed ownership. Manual ID collisions
    return 409; overlapping filters return 400. Nonempty configuration
    requires object storage enabled and an async-invocation eligible plan.
    Accepted deliveries retain their original destination snapshots.

    Args:
        slug (str):
        bucket (UUID):
        body (ReconcileObjectStorageComputeTriggersRequest): Revision-checked replacement of only
            the bucket's declarative compute triggers.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectBucketNotifications | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    bucket: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ReconcileObjectStorageComputeTriggersRequest,
) -> ObjectBucketNotifications | Problem | None:
    """Reconcile managed ObjectCreated triggers for the bucket's owning app

     Requires MFA, storage manage scope and bucket write access. The destination
    is always this bucket's owning app in its region. expected_revision is
    mandatory and stale revisions return 409, including unchanged requests.
    Replaces only managed rules, preserving manual notification rules.
    Explicit triggers: [] clears managed ownership. Manual ID collisions
    return 409; overlapping filters return 400. Nonempty configuration
    requires object storage enabled and an async-invocation eligible plan.
    Accepted deliveries retain their original destination snapshots.

    Args:
        slug (str):
        bucket (UUID):
        body (ReconcileObjectStorageComputeTriggersRequest): Revision-checked replacement of only
            the bucket's declarative compute triggers.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectBucketNotifications | Problem
    """

    return sync_detailed(
        slug=slug,
        bucket=bucket,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    bucket: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ReconcileObjectStorageComputeTriggersRequest,
) -> Response[ObjectBucketNotifications | Problem]:
    """Reconcile managed ObjectCreated triggers for the bucket's owning app

     Requires MFA, storage manage scope and bucket write access. The destination
    is always this bucket's owning app in its region. expected_revision is
    mandatory and stale revisions return 409, including unchanged requests.
    Replaces only managed rules, preserving manual notification rules.
    Explicit triggers: [] clears managed ownership. Manual ID collisions
    return 409; overlapping filters return 400. Nonempty configuration
    requires object storage enabled and an async-invocation eligible plan.
    Accepted deliveries retain their original destination snapshots.

    Args:
        slug (str):
        bucket (UUID):
        body (ReconcileObjectStorageComputeTriggersRequest): Revision-checked replacement of only
            the bucket's declarative compute triggers.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectBucketNotifications | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    bucket: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ReconcileObjectStorageComputeTriggersRequest,
) -> ObjectBucketNotifications | Problem | None:
    """Reconcile managed ObjectCreated triggers for the bucket's owning app

     Requires MFA, storage manage scope and bucket write access. The destination
    is always this bucket's owning app in its region. expected_revision is
    mandatory and stale revisions return 409, including unchanged requests.
    Replaces only managed rules, preserving manual notification rules.
    Explicit triggers: [] clears managed ownership. Manual ID collisions
    return 409; overlapping filters return 400. Nonempty configuration
    requires object storage enabled and an async-invocation eligible plan.
    Accepted deliveries retain their original destination snapshots.

    Args:
        slug (str):
        bucket (UUID):
        body (ReconcileObjectStorageComputeTriggersRequest): Revision-checked replacement of only
            the bucket's declarative compute triggers.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectBucketNotifications | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            bucket=bucket,
            client=client,
            body=body,
        )
    ).parsed
