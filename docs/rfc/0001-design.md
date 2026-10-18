# RFC 0001: ephemeral-test-envs design

- **Status:** Accepted (M1 built)
- **Author:** EquinoxWN
- **Created:** 2026-10-18

## Problem (Define)

Integration bugs (a chart value that does not match the code, seed data the service refuses, a pod
that cannot start under a restricted security context) usually show up in a shared staging cluster
after merge. Staging is shared, so one broken branch blocks everyone, and it often holds copies of
production data. Teams need every pull request to bring up the whole system on its own, with
realistic data that contains no personal data, and to leave nothing running.

Users: developers and reviewers of a service that ships as container images and a Helm chart;
platform engineers who own the CI pipeline.

### Critical-to-quality measures

| CTQ | Target | How it is measured |
|---|---|---|
| Isolation | One cluster per pull request, own kubeconfig, no shared state | `envplan` tests: names derive from the PR number, every helm and kubectl call carries the environment's own kubeconfig |
| Always torn down | Teardown runs after success, failure and cancellation | `envplan` tests with a fake executor; CI report field `torn_down` |
| No personal data in seed | 0 leak-scan findings; every quasi-identifier group has at least k=5 customers | `etenv scan`, `anon` tests over 25 generated datasets, the API refusing non-anonymized data |
| Seed loaded exactly | Count and SHA-256 checksum of the loaded data equal the seed file | `helm test` pod (`seeder verify`), seeder end-to-end test |
| Least privilege | 0 chart policy violations (non-root, read-only root, no capabilities, limits, probes, NetworkPolicy, no API token) | `helm template \| etenv policy` in CI |
| Time to a ready environment | Recorded per run (`up_seconds`) | CI notice and `.tmp/env-report.json`; first numbers come from CI (M1 measures, M3 improves) |

## Goals

- M1: when a pull request opens, CI starts a kind cluster on the runner, builds the PR's images,
  loads them into kind and installs the Helm chart with anonymized seed data, then checks the data
  with `helm test` and deletes the cluster.
- The seed data is produced from production-shaped data by a keyed, deterministic anonymizer, and
  the service refuses anything that is not in the anonymized shape.
- Every command is built from validated values and run without a shell.

## Non-goals

- Playwright end-to-end tests, PR comments and artifacts (M2), image and Helm caching (M3).
- Long-lived preview environments with public URLs.
- Anonymizing arbitrary databases: the anonymizer covers this sample schema; the approach carries
  over, the field rules do not.
- Formal privacy guarantees beyond k-anonymity on the chosen quasi-identifiers (no differential
  privacy).

## Design

```
pull_request -> ephemeral-env workflow -> etenv up -pr N -commit SHA
  1 kind create cluster   node image pinned by digest, API server on 127.0.0.1, own kubeconfig in .tmp/envs
  2 docker build          --target api and --target seeder (distroless nonroot, base images by digest)
  3 kind load             both images; the chart never pulls them (imagePullPolicy: Never)
  4 helm install --wait   values.schema.json; a post-install Job loads the seed
  5 helm test             a pod checks counts, checksum and one customer read back
  6 kubectl get           inventory for the log
  on failure: kubectl get events, kubectl logs
  always: kind delete cluster   -> report JSON + ::notice or ::error annotation
```

- `internal/dataset`: two shapes. `Raw` has the personal fields; `Seed` has pseudonyms and
  generalized values only. Strict JSON decoding (unknown fields refused, 4 MiB cap) means raw data
  cannot be loaded by mistake: its extra fields fail decoding.
- `internal/anon`: HMAC-SHA256 with a secret key (at least 32 bytes) gives stable pseudonyms, so
  an order still points to its customer. Names, emails and phones become `Customer 3fa9c1`,
  `u-…@example.invalid` and the fictional `+1-555-01xx` range. Street, city, card digits and free
  text are dropped. Country, postal code and birth date are generalized (two-letter code, first two
  characters, decade). Dates move back by a key-derived 1 to 365 days. Customers in groups (same
  country, postal prefix and decade) smaller than k lose their postal prefix, then their country,
  and any customer still in a small group is suppressed with their orders. A scan then searches
  the output for every raw value and for email, phone and card-number patterns.
- `internal/api`: the sample service. `PUT /api/dataset` replaces all data atomically (so the seed
  Job can retry safely), `GET /api/stats` reports counts and the canonical SHA-256 checksum.
- `internal/seeder`: `load` for the post-install Job, `verify` for `helm test`, with bounded retries.
- `internal/envplan`: builds the argv plan from a validated config and runs it; diagnostics and
  teardown use a context detached from cancellation, so the cluster is deleted even when the job
  is cancelled.
- `internal/chartpolicy`: checks the rendered chart before any cluster exists.

## Alternatives considered

| Option | Why not (yet) |
|---|---|
| Shared long-lived cluster with a namespace per PR (or vcluster) | Faster start, but needs a cluster to run and pay for, cluster-scoped objects collide, and a crashed job leaves namespaces behind. See [ADR 0002](../adr/0002-kind-cluster-per-pull-request.md). |
| A managed cloud cluster per PR | Closest to production, but minutes of provisioning, cloud credentials in CI, and cost per PR. |
| docker compose instead of Kubernetes | Simple and fast, but does not test the Helm chart, security contexts, NetworkPolicy or probes, which is where many deployment bugs live. |
| Synthetic seed data only (no anonymizer) | No privacy risk, but loses the shape and skew of real data; the anonymizer keeps links and distributions. See [ADR 0003](../adr/0003-keyed-pseudonyms-and-k-groups.md). |
| Masking with random values per run | Breaks links between tables and makes runs non-reproducible. |

## Measurement plan (Measure)

- Local: unit and end-to-end tests of every package (`go test`), the leak scan and group sizes of
  the committed seed, the policy check of the rendered chart, govulncheck.
- CI: `etenv up` writes `up_seconds`, `total_seconds`, every step's duration and `torn_down` into
  the report and prints them as a `::notice` (or `::error`) annotation.
- M3 compares total pipeline time before and after image and Helm caching.

## Milestones

- **M1 (done):** RFC; CI job starts a kind cluster; PR images built and loaded; chart installed with
  anonymized seed data; seed verified by `helm test`; teardown.
- **M2:** Playwright tests against the whole system with screenshots and traces; results posted as
  a PR comment and stored as build artifacts.
- **M3:** caching of images and Helm dependencies; discarded runner; before/after pipeline times.

## Risks and open questions

- The kind run is verified in CI only; Docker was not used on the development machine for this
  repo. The policy check, the values schema and the Go tests cover what can be checked without a
  cluster.
- kindnet's NetworkPolicy support decides whether the API's ingress and egress rules are enforced;
  on another CNI that filters kubelet probes, the policy would need a node CIDR rule.
- The API keeps data in memory: if its pod restarts after seeding, the data is gone until the Job
  runs again (`helm upgrade`). A database is out of scope for M1.
- k-anonymity on three quasi-identifiers does not protect against every linkage attack; real data
  needs a review of which columns count as quasi-identifiers.
