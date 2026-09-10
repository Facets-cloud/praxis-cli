# Secret value migration

Map workload-required values from AWS Secrets Manager to existing GCP Secret
Manager objects. Raptor/IaC owns target object existence, policy and references;
this workflow adds **value versions**, not new secret objects. Derive scope from
ESO `ExternalSecret` remoteRef/dataFrom and workload secretKeyRef/envFrom use,
not every secret in an account. Inspect key names/metadata without dumping values.

This is an operator-run data path with explicitly authorized value access. A
read-only discovery role commonly lacks AWS GetSecretValue. Do not bypass that
boundary or mint credentials for the agent. The operator needs source value-read,
target describe, add-version **and read-back access**; version-adder alone cannot
verify. Confirm exact AWS account/region and GCP project/secret IDs first.

Use [mapping example](../scripts/secrets/mapping.example.csv) and
[replicate.py](../scripts/secrets/replicate.py). Read the helper before use:

```bash
python3 PATH_TO_SKILL/scripts/secrets/replicate.py --mapping mapping.csv \
  --gcp-project PROJECT --aws-profile PROFILE --aws-region REGION --dry-run
```

Dry-run reads real source values, classifies them and checks target existence;
it does not write versions. Value-read permission and handling still matter.
Without `--dry-run`, the helper writes versions. It passes value bytes to gcloud
on stdin, not argv/files; keep tracing and verbose subprocess logs off. Review
error output before sharing: provider errors can contain sensitive context.

| Classification | Meaning |
|---|---|
| ok | JSON object with string values; suitable shape for the intended ESO extract mapping |
| nonflat | JSON object with non-string values; copied, but ESO extraction needs non-prod verification |
| NEEDS-MANUAL | Binary/non-JSON/scalar/array needs explicit mapping or different consumption contract |

The implementation permits nonflat objects; old prose claiming all are rejected
is incorrect. `--allow-plaintext` broadens accepted strings but does not make them
ESO-extractable. Don't set it to silence unresolved mapping failures.

Targets must already exist under the right replication/residency policy. A
failed describe can be permission failure, not absence; do not auto-create.
The preserved helper prints "does not exist" for any failed describe, and its
header lists version-adder without all read permissions. Interpret that output
as **existence unverified** until the actual provider error is classified; this
reference's permission requirements take precedence over that legacy header.
Default skip-unchanged avoids version churn; force creates another version.
The helper compares short SHA digests and reads `latest`, not a pinned newly
created version. Its result is useful under a quiescent target but is not strict
byte-level proof under concurrent writers. Coordinate writers and independently
verify the exact version bytes without exposing them where that guarantee is
required. Never log hashes of individual low-entropy values as public evidence.

Verify resulting ESO synchronization, expected Kubernetes key sets and workload
access separately. Equal provider bytes do not establish correct secret wiring.
Report each mapping's status; ERROR/VERIFY-FAIL/NEEDS-MANUAL is nonzero and must
remain visible. A partial batch is not all-or-nothing: reconcile completed targets
before retrying. Preserve source values and old target versions; rollback means
selecting an appropriate prior version/wiring, not deleting source secrets.
