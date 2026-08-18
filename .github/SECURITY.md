# Security Policy

## Reporting a vulnerability

Please do not report security vulnerabilities through public issues.

Use GitHub's private vulnerability reporting: **Security tab → Report a
vulnerability**. Reports go directly to the maintainer and are not visible
publicly until a fix is released.

Please include:

- The component involved (gateway backend, admin/portal frontend, deployment
  configuration).
- Steps to reproduce or a proof of concept.
- Affected version or commit hash.
- Any impact assessment you have (auth bypass, billing integrity, data
  exposure).

## Scope

The gateway itself, its deployment templates (`deploy/`), and the container
images built from this repository. Vulnerabilities in upstream AI providers or
in dependencies should be reported to the respective upstream project; we will
handle dependency upgrades on request.

## Response expectations

- Acknowledgment within 7 days.
- Assessment and a fix timeline after reproduction.
- Credit in the release notes if you wish.

## Handling of upstream channel keys

Note for operators: channel upstream API keys are encrypted at rest with a key
derived from `TZL_ENCRYPT_KEY`. That key is out of scope for encryption
protection — treat database backups and the key file with equal care
(`deploy/backup-secrets.sh`).
