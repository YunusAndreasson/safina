# Security policy

Safina runs with your kubeconfig and can do whatever your RBAC allows, so a
hole in safina is a hole in your cluster access. Please report one privately.

## Reporting a vulnerability

Use GitHub's private reporting:
[report a vulnerability](https://github.com/YunusAndreasson/safina/security/advisories/new).
Please don't open a public issue.

Include the version (`safina --version`), what you did, what happened, and
what an attacker would gain. A fix is coordinated with you before anything is
published, and you're credited in the advisory unless you'd rather not be.

## Supported versions

Only the latest release gets fixes; they ship as a new release.

## Scope

In scope:

- the `safina` binary, including `safina mcp`
- the install script at https://safina.run/install.sh and the release archives
- this repository's workflows

Out of scope: vulnerabilities in Kubernetes itself, in your cluster's
configuration, or in the `claude` CLI that chat runs through. Report those to
their projects.

## Verifying a download

Each release lists the SHA-256 of its archives in `checksums.txt`, which the
install script checks. Releases after v0.1.0 also carry signed build
provenance:

```sh
gh attestation verify safina_linux_amd64.tar.gz -R YunusAndreasson/safina
```
