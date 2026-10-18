# ADR 0002: A kind cluster per pull request on the CI runner

- **Status:** Accepted

## Context

Each pull request needs the whole system (both images, the Helm chart, seed data) running in
Kubernetes, isolated from every other pull request, and gone afterwards. Two common layouts:

1. One shared, long-lived cluster; each pull request gets a namespace (or a vcluster) and a job
   deletes it when the pull request closes.
2. A throwaway cluster created inside the CI job itself.

## Decision

`etenv up` creates a kind cluster on the GitHub-hosted runner for every run, named after the pull
request (`etenv-pr-<n>`), with its own kubeconfig in `.tmp/envs/` and the API server bound to
127.0.0.1. The PR's images are loaded into the node with `kind load docker-image` and never pushed
to a registry. Teardown (`kind delete cluster`) always runs, with a context that survives job
cancellation, and the runner itself is discarded when the job ends.

## Consequences

- Nothing to operate or pay for between runs; no registry credentials, no cloud credentials, no
  kubeconfig secrets in CI. A crashed job cannot leave a namespace behind in a shared cluster.
- Cluster-scoped objects (CRDs, ClusterRoles) can be tested without colliding with other pull
  requests.
- Every run pays the cluster start-up time and builds images from scratch. M1 records the time per
  step; M3 adds image and Helm caching to bring it down.
- The environment is limited to one runner (2 to 4 CPUs, 7 to 16 GB): fine for a service and its
  seed, not for load tests or large datasets.
- The node image is pinned by digest to the kind v0.33.0 release (`kindest/node:v1.37.0@sha256:…`),
  so a kind or Kubernetes upgrade is an explicit change.
