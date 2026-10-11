# Recover a workflow export

Recovery is an account operator action. The customer UI shows the operation ID
and asks for review when work requires reconciliation; it does not repeat business
work or expose account credentials. Notification failure leaves a completed export
downloadable and uses its own delivery retry policy.

## Inspect before deciding

```sh
gregale customer-operations get OPERATION_UUID --app exports --json
gregale customer-operations inspect OPERATION_UUID --app exports --json
gregale customer-operations executions OPERATION_UUID --app exports --json
```

Record the observed generation and inspection revision. Inspect which steps were
confirmed, which final attempt is uncertain, and whether its private file was
verified and retained. File retention alone does not confirm final-step success.
The sample collect/transform steps are pure computations; a customized provider
action needs provider evidence before repeating an uncertain effect.

Read the proposed resume plan without starting work:

```sh
gregale customer-operations recover OPERATION_UUID --app exports \
  --expected-generation 1 --resolution safe_to_retry --preview --json
```

Use the generation actually inspected. Preview is an observation and does not
reserve quota or authorize replay. Stop if a blocker or changed inspection makes
the proposed resume inappropriate.

## Apply one evidenced decision

After verifying that retry is safe, write the findings to a private evidence file.
Choose a stable decision ID and apply the inspection fence:

```sh
gregale customer-operations recover OPERATION_UUID --app exports \
  --expected-generation 1 --resolution safe_to_retry \
  --inspection-revision sha256:INSPECTION_HASH \
  --recovery-id export-review-42 --evidence-file ./verified-evidence.txt \
  --receipt-file ./export-recovery.json --json
```

Approved workflow resume advances the generation while retaining the same
operation/run, original code/input and confirmed prefix. It executes only the
unfinished action. The final action uses stable report ID `export-csv`, filename
and bytes; its fresh native proof reuses a retained verified copy without a second
transfer. It must still confirm success before that file becomes downloadable.
Do not invoke direct native workflow resume or step retry to bypass this decision.

If the recovery response is lost, resume the exact saved request:

```sh
gregale customer-operations recover OPERATION_UUID --app exports \
  --receipt-file ./export-recovery.json --json
gregale customer-operations get OPERATION_UUID --app exports --json
```

The saved request and `.decided.json` acknowledgement are private. The CLI preserves
the decision ID, evidence and fingerprint; it does not create another decision.
The acknowledgement describes past acceptance. Inspect current status separately.
If success is already confirmed by other evidence, use the explicit `succeeded`
resolution with verified typed output instead of retrying business work. Failed
or cancelled resolutions also require evidence of that outcome.

Closing new admission does not remove recovery access for accepted work. This
starter does not change admission or qualify native execution, provider behavior
or fleet rollout. Follow the platform's native qualification and rollback guides
before activating any workflow customer cohort.
