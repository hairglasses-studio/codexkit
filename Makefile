SHELL := /usr/bin/bash

.PHONY: baseline baseline-strict build check ci test vet

ci check:
	bash scripts/local-ci.sh

baseline:
	bash scripts/local-baseline.sh

baseline-strict:
	GOWORK=off go run ./cmd/codexkit baseline check .

build:
	GOWORK=off go build ./...

test:
	GOWORK=off go test -count=1 -race ./...

vet:
	GOWORK=off go vet ./...
