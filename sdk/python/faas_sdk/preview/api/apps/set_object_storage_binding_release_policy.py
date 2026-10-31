from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.binding_release_policy import BindingReleasePolicy
from ...models.problem import Problem
from ...models.set_object_storage_binding_release_policy_request import SetObjectStorageBindingReleasePolicyRequest
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: SetObjectStorageBindingReleasePolicyRequest,
    scope: str | Unset = "default",
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    params: dict[str, Any] = {}

    params["scope"] = scope

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/bindings/release-policy-with-object-storage".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> BindingReleasePolicy | Problem | None:
    if response.status_code == 200:
        response_200 = BindingReleasePolicy.from_dict(response.json())

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[BindingReleasePolicy | Problem]:
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
    body: SetObjectStorageBindingReleasePolicyRequest,
    scope: str | Unset = "default",
) -> Response[BindingReleasePolicy | Problem]:
    """Replace release policy including required object-storage operations.

     Requires deploy:write or admin and completed MFA. Uses the same revision
    compare-and-set and traffic fences as release-policy. Explicit
    object_storage_requirements is required; an empty object clears it.
    Keys are managed environment prefixes in this scope (at most 32), values
    are read, write or read_write. Missing bindings, insufficient permissions,
    stale evidence, incomplete stages and mismatched operations block traffic.
    Read accepts the legacy listing canary; write requires upload and cleanup;
    read_write also requires byte-for-byte download. Current application ACKs
    are independently required when configured. This call only records policy;
    neither policy updates nor promotion initiate storage writes. Old servers
    return 404 on this route rather than silently dropping the requirements.

    Args:
        slug (str):
        scope (str | Unset):  Default: 'default'.
        body (SetObjectStorageBindingReleasePolicyRequest): Revision-guarded release policy
            replacement with explicit required object-storage canary modes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BindingReleasePolicy | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        scope=scope,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetObjectStorageBindingReleasePolicyRequest,
    scope: str | Unset = "default",
) -> BindingReleasePolicy | Problem | None:
    """Replace release policy including required object-storage operations.

     Requires deploy:write or admin and completed MFA. Uses the same revision
    compare-and-set and traffic fences as release-policy. Explicit
    object_storage_requirements is required; an empty object clears it.
    Keys are managed environment prefixes in this scope (at most 32), values
    are read, write or read_write. Missing bindings, insufficient permissions,
    stale evidence, incomplete stages and mismatched operations block traffic.
    Read accepts the legacy listing canary; write requires upload and cleanup;
    read_write also requires byte-for-byte download. Current application ACKs
    are independently required when configured. This call only records policy;
    neither policy updates nor promotion initiate storage writes. Old servers
    return 404 on this route rather than silently dropping the requirements.

    Args:
        slug (str):
        scope (str | Unset):  Default: 'default'.
        body (SetObjectStorageBindingReleasePolicyRequest): Revision-guarded release policy
            replacement with explicit required object-storage canary modes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BindingReleasePolicy | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
        scope=scope,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetObjectStorageBindingReleasePolicyRequest,
    scope: str | Unset = "default",
) -> Response[BindingReleasePolicy | Problem]:
    """Replace release policy including required object-storage operations.

     Requires deploy:write or admin and completed MFA. Uses the same revision
    compare-and-set and traffic fences as release-policy. Explicit
    object_storage_requirements is required; an empty object clears it.
    Keys are managed environment prefixes in this scope (at most 32), values
    are read, write or read_write. Missing bindings, insufficient permissions,
    stale evidence, incomplete stages and mismatched operations block traffic.
    Read accepts the legacy listing canary; write requires upload and cleanup;
    read_write also requires byte-for-byte download. Current application ACKs
    are independently required when configured. This call only records policy;
    neither policy updates nor promotion initiate storage writes. Old servers
    return 404 on this route rather than silently dropping the requirements.

    Args:
        slug (str):
        scope (str | Unset):  Default: 'default'.
        body (SetObjectStorageBindingReleasePolicyRequest): Revision-guarded release policy
            replacement with explicit required object-storage canary modes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BindingReleasePolicy | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        scope=scope,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetObjectStorageBindingReleasePolicyRequest,
    scope: str | Unset = "default",
) -> BindingReleasePolicy | Problem | None:
    """Replace release policy including required object-storage operations.

     Requires deploy:write or admin and completed MFA. Uses the same revision
    compare-and-set and traffic fences as release-policy. Explicit
    object_storage_requirements is required; an empty object clears it.
    Keys are managed environment prefixes in this scope (at most 32), values
    are read, write or read_write. Missing bindings, insufficient permissions,
    stale evidence, incomplete stages and mismatched operations block traffic.
    Read accepts the legacy listing canary; write requires upload and cleanup;
    read_write also requires byte-for-byte download. Current application ACKs
    are independently required when configured. This call only records policy;
    neither policy updates nor promotion initiate storage writes. Old servers
    return 404 on this route rather than silently dropping the requirements.

    Args:
        slug (str):
        scope (str | Unset):  Default: 'default'.
        body (SetObjectStorageBindingReleasePolicyRequest): Revision-guarded release policy
            replacement with explicit required object-storage canary modes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BindingReleasePolicy | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            scope=scope,
        )
    ).parsed
