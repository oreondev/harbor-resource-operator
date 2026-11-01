# Security policy

The operator holds credentials for Harbor (often an administrator or a system robot) and writes robot account secrets into Kubernetes, so security reports are taken seriously.

## Reporting a vulnerability

Please do **not** open a public issue. Report privately by either:

- GitHub: the repository's **Security** tab → **Report a vulnerability**, or
- e-mail: fahri.cap@oreonit.com

Include the version, how to reproduce, and the impact you see. You will get an acknowledgement within 5 working days and an assessment within 14 days. Fixes are released as a patch version, and the advisory is published once a fix is available; reporters are credited unless they ask not to be.

## Supported versions

Only the latest minor release gets security fixes while the project is at `0.x`.

## Scope

In scope: the operator code, its container image, the Helm chart and `install.yaml` published from this repository. Vulnerabilities in Harbor itself go to the [Harbor project](https://github.com/goharbor/harbor/security/policy).
