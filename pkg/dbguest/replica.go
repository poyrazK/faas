package dbguest

// ReplicaOnlyRolePrefix marks native logins issued for standby-only routing.
const ReplicaOnlyRolePrefix = "gregale_replica_"

// ReplicaOnlySuffix follows the database ID in a standby-only endpoint label.
const ReplicaOnlySuffix = "-replica"
