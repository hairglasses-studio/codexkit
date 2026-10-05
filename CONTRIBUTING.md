# Contributing to codexkit

Thank you for your interest in contributing.

## Development

```bash
make ci
```

CI runs on GitHub Actions (required check `ci / gate`); `make ci` runs the same
gate locally.
`make baseline-strict` is available when no managed launcher overlay is present.

## Pull Requests

- Fork the repo and create a feature branch
- Follow existing code conventions (Go 1.26+, `ToolModule` interface)
- Add tests for new functionality
- Run `make ci` before submitting
- Do not add credentials, private repo inventory, host-specific state, tenant data, or personal data
- Run `gitleaks detect --source . --no-git --redact` for changes touching examples, fixtures, workflow files, or generated artifacts

## Issues

Bug reports and feature requests are welcome via GitHub Issues.
