from typing import Literal

DataUpstreamHistoryResponseKind = Literal[
    "cassandra",
    "clickhouse",
    "elasticsearch",
    "etcd",
    "https_api",
    "kafka",
    "memcached",
    "minio",
    "mongo",
    "nats",
    "opensearch",
    "postgres",
    "rabbitmq",
    "redis",
    "s3",
]

DATA_UPSTREAM_HISTORY_RESPONSE_KIND_VALUES: set[DataUpstreamHistoryResponseKind] = {
    "cassandra",
    "clickhouse",
    "elasticsearch",
    "etcd",
    "https_api",
    "kafka",
    "memcached",
    "minio",
    "mongo",
    "nats",
    "opensearch",
    "postgres",
    "rabbitmq",
    "redis",
    "s3",
}


def check_data_upstream_history_response_kind(value: str) -> DataUpstreamHistoryResponseKind:
    if value in DATA_UPSTREAM_HISTORY_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_UPSTREAM_HISTORY_RESPONSE_KIND_VALUES!r}")
