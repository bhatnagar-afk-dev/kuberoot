# Security Policy

KubeRoot handles kubeconfigs and cluster credentials, so we take security seriously.

## Supported versions

Until v1.0, only the latest release receives security fixes.

## Reporting a vulnerability

Please report vulnerabilities privately using
[GitHub's private vulnerability reporting](https://github.com/bhatnagar-afk-dev/kuberoot/security/advisories/new).
Do not open a public issue.

Include, where possible: affected version, platform, reproduction steps, and impact.
We aim to acknowledge reports within 3 business days and to agree on a disclosure timeline
with you.

## Scope notes

- KubeRoot never sends kubeconfigs, credentials, or cluster data to any third-party service.
- Credentials are used only to talk to the clusters you configure, via client-go.
