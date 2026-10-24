//go:build !linux && !darwin

package workspace

import "errors"

func ownedAliasManifest(string) error {
	return errors.New("alias reconciliation unsupported on this platform")
}
func lockAliasManifest(string) (func(), error) {
	return nil, errors.New("alias reconciliation unsupported on this platform")
}
