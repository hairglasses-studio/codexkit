## Summary

- 

## Verification

- [ ] `go build ./...`
- [ ] `go vet ./...`
- [ ] `go test -count=1 -race ./...`
- [ ] `gitleaks detect --source . --no-git --redact`

## Public Boundary

- [ ] This change does not add credentials, private repo inventory, private hostnames, personal data, tenant data, or internal workflow details.
