# Security Policy

## Reporting a vulnerability

Please report vulnerabilities privately through
[GitHub private vulnerability reporting](https://github.com/sliplane/cli/security/advisories/new).
Do not open public issues, discussions, or pull requests for security problems.

Include the CLI version (`sliplane version`), your operating system, steps to
reproduce or a proof of concept, and the impact you expect. We aim to
acknowledge reports within three business days and will keep you updated
until the issue is resolved. Please give us a reasonable chance to release a
fix before disclosing the issue publicly.

## Supported versions

Fixes are released for the latest version only. Update by rerunning the
[install command](README.md#installation).

## Scope

This repository contains the Sliplane CLI. We especially want to hear about
issues that let an attacker, without the victim's credentials or access to
their machine:

- obtain a Sliplane API token, OAuth authorization code, S3 secret, database
  password, or temporary SSH key, for example through the browser login
  callback or output that should be redacted;
- send credentials to a server the user did not choose;
- make `install.sh` install a release that was not signed by a trusted
  maintainer key, or a file that does not match `checksums.txt`;
- write files outside the destination the user chose, for example through S3
  object names during a download;
- inject terminal control sequences through remote data such as service
  names or logs.

Vulnerabilities in the hosted Sliplane platform or API are also welcome
through the same form; say which part you tested.

A dependency with a known CVE is not necessarily a vulnerability in the CLI.
CI runs [`govulncheck`](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck),
which checks whether affected code is reachable. When reporting a dependency
CVE, please show how it can be exploited through the CLI.
