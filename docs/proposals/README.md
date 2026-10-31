# Design Proposals

Significant features are designed here before they are built, in the spirit of Kubernetes
KEPs but lighter-weight.

1. Copy [0000-template.md](0000-template.md) to `NNNN-short-title.md` (next free number).
2. Open a PR with status `Provisional`. Discussion happens on the PR.
3. A maintainer merges it as `Implementable` once there is agreement (minimum 5 days open,
   unless trivial). Implementation PRs reference the proposal.
4. Update the status to `Implemented` when the feature ships.

## Index

| # | Title | Status |
|---|-------|--------|
| [0001](0001-cluster-management.md) | Cluster management | Implementable |
