# Security policy

## Reporting a vulnerability

Please report security problems privately through GitHub:
**Security > Report a vulnerability** on this repository
(`https://github.com/EquinoxWN/ephemeral-test-envs/security/advisories/new`). Do not open a public issue.

Include what you found, how to reproduce it, and the impact you expect. You will get an answer
within 7 days. Fixes are released as soon as they are ready, and you are credited unless you ask
not to be.

## Supported versions

This project is pre-1.0. Only the latest commit on `main` receives fixes.

## Scope

- **In scope:** personal data surviving anonymization or reaching an environment, commands built
  from pull request data that could run something other than kind, docker, helm or kubectl, an
  environment left running after a job, chart settings that weaken pod isolation, and unbounded
  input to the API or the CLI.
- **Out of scope:** the synthetic fixtures (they contain no real people) and the demo key that
  anonymizes them.

## How this repository protects itself

- Every command is an argv list built from validated values (PR number, hex commit, digest-pinned
  node image) and run without a shell.
- Each environment has its own kubeconfig and Helm homes inside the repository; teardown always runs.
- The kind API server listens on 127.0.0.1; the Service is ClusterIP only; a NetworkPolicy lets only
  the seeder reach the API and the API reach nothing.
- Pods run as non-root (uid 65532) with a read-only root filesystem, no capabilities, no privilege
  escalation, the default seccomp profile, no service account token, and CPU and memory limits.
- Images are distroless, built from base images pinned by digest; kind, Helm and kubectl are
  downloaded from official releases and checked against pinned SHA-256 values.
- Seed data is keyed pseudonyms and generalized values; the API refuses any other shape.
- Every GitHub Action is pinned to a full commit SHA, and workflows run with read-only
  permissions and without persisted credentials.
- Dependabot update pull requests are paused (`open-pull-requests-limit: 0`) so the repository keeps a single `main` branch; dependencies are updated by hand and checked by the CI audit.
- CI runs lint, tests and a known-vulnerability check (govulncheck) on every push and pull request.
