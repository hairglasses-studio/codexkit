package agentorchestration

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// ScopeCollision describes an intersection between two lexical scopes.
type ScopeCollision struct {
	Left  PathScope `json:"left"`
	Right PathScope `json:"right"`
}

// NormalizePathScope returns a portable slash-separated lexical scope. It
// rejects traversal, repository-wide roots, and invalid kinds so task bounds
// cannot silently broaden during cleaning.
func NormalizePathScope(scope PathScope) (PathScope, error) {
	raw := strings.TrimSpace(strings.ReplaceAll(scope.Path, "\\", "/"))
	if raw == "" {
		return PathScope{}, fmt.Errorf("scope path is empty")
	}
	for _, component := range strings.Split(raw, "/") {
		if component == ".." {
			return PathScope{}, fmt.Errorf("scope path contains traversal: %q", scope.Path)
		}
	}
	clean := path.Clean(raw)
	if clean == "." || clean == "/" {
		return PathScope{}, fmt.Errorf("scope path is unbounded: %q", scope.Path)
	}
	if scope.Kind != ScopeFile && scope.Kind != ScopeDirectory {
		return PathScope{}, fmt.Errorf("invalid scope kind %q", scope.Kind)
	}
	return PathScope{Path: clean, Kind: scope.Kind}, nil
}

// ScopesCollide reports whether two normalized file/directory ownership
// boundaries intersect.
func ScopesCollide(left, right PathScope) (bool, error) {
	left, err := NormalizePathScope(left)
	if err != nil {
		return false, err
	}
	right, err = NormalizePathScope(right)
	if err != nil {
		return false, err
	}
	if left.Path == right.Path {
		return true, nil
	}
	if left.Kind == ScopeDirectory && containsPath(left.Path, right.Path) {
		return true, nil
	}
	if right.Kind == ScopeDirectory && containsPath(right.Path, left.Path) {
		return true, nil
	}
	return false, nil
}

// DetectScopeCollisions returns all lexical intersections in stable order.
func DetectScopeCollisions(left, right []PathScope) ([]ScopeCollision, error) {
	left, err := canonicalScopes(left)
	if err != nil {
		return nil, err
	}
	right, err = canonicalScopes(right)
	if err != nil {
		return nil, err
	}
	var collisions []ScopeCollision
	for _, a := range left {
		for _, b := range right {
			collides, err := ScopesCollide(a, b)
			if err != nil {
				return nil, err
			}
			if collides {
				collisions = append(collisions, ScopeCollision{Left: a, Right: b})
			}
		}
	}
	sort.Slice(collisions, func(i, j int) bool {
		if collisions[i].Left.Path != collisions[j].Left.Path {
			return collisions[i].Left.Path < collisions[j].Left.Path
		}
		if collisions[i].Right.Path != collisions[j].Right.Path {
			return collisions[i].Right.Path < collisions[j].Right.Path
		}
		if collisions[i].Left.Kind != collisions[j].Left.Kind {
			return collisions[i].Left.Kind < collisions[j].Left.Kind
		}
		return collisions[i].Right.Kind < collisions[j].Right.Kind
	})
	return collisions, nil
}

func canonicalScopes(scopes []PathScope) ([]PathScope, error) {
	seen := make(map[string]struct{}, len(scopes))
	result := make([]PathScope, 0, len(scopes))
	for _, scope := range scopes {
		normalized, err := NormalizePathScope(scope)
		if err != nil {
			return nil, err
		}
		key := string(normalized.Kind) + "\x00" + normalized.Path
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, normalized)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Path == result[j].Path {
			return result[i].Kind < result[j].Kind
		}
		return result[i].Path < result[j].Path
	})
	return result, nil
}

func containsPath(parent, candidate string) bool {
	if parent == candidate {
		return true
	}
	return strings.HasPrefix(candidate, strings.TrimSuffix(parent, "/")+"/")
}
