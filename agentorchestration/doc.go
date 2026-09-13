// Package agentorchestration provides pure, deterministic admission policy for
// routing bounded tasks between frontier and Spark model lanes.
//
// This package does not describe official OpenAI product behavior. In
// particular, DefaultConfig thresholds, concurrency caps, lane names, and
// safety rules are hairglasses-studio harness policy. Callers must obtain
// current model availability, quota, and capacity through an authorized
// integration and pass sanitized snapshots into this package.
//
// The package deliberately performs no subprocess execution, account access,
// network requests, filesystem writes, publishing, or destructive operations.
package agentorchestration
