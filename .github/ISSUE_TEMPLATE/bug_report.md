---
name: Bug report
about: Report something that works incorrectly
title: ''
labels: bug
assignees: ''
---

**Describe the bug**

A clear and concise description of what is wrong and what you expected to happen instead.

**To reproduce**

Steps to reproduce the behavior:

1.
2.
3.

**Area**

Where does the problem appear? For example: relay (`/v1` calls), billing/credits, admin frontend, portal frontend, deployment/operations.

**Environment**

- Deployment mode: [single binary / Docker Compose]
- Go version (source builds): [output of `go version`]
- PostgreSQL version: [e.g. 16]
- Token Zen Lite version/commit: [output of `tzl version`, or image tag/commit]
- Relevant config (redact secrets): [env vars such as `TZL_ENV`, `TZL_UPSTREAM_TIMEOUT_SEC`]

**Logs and evidence**

Relevant log excerpts, request/response samples, or error messages. Please redact API keys and other secrets.

**Tests**

Does `make test` pass on your checkout? [yes / no / not run — note any failures]
