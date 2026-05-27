# Security

Please report security issues through GitHub security advisories or maintainer contact. Do not open public issues for suspected vulnerabilities.

This project should not contain real credentials, tokens, cookies, private repo inventory, host-specific operational state, tenant data, or personal data. If you find that material in the repository, report it as a security issue.

Before publishing changes that touch examples, fixtures, workflow files, or generated artifacts, run:

```bash
gitleaks detect --source . --no-git --redact
```
