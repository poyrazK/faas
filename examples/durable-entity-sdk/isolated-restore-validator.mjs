// ADR-854: bundle this entrypoint separately from the production application.
// Match these rules to your deployed application's schema. No imports or I/O.
export default function validateRestore(input) {
  const candidate = input?.candidate;
  const valid = input?.protocol_version === 1 &&
    input?.event === 'validate_restore' &&
    input?.entity?.namespace === 'counter' &&
    candidate?.schema_version === 1 &&
    Number.isSafeInteger(candidate?.data?.count) && candidate.data.count >= 0;
  return { protocol_version: 1, valid };
}
