//go:build !linux && !darwin

package harnessfs

import "runtime"

type Root struct{}
type Stage struct{}
type SymlinkStage struct{}
type Hold struct{}
type Quarantine struct{}

func unsupported() error {
	return &CapabilityError{GOOS: runtime.GOOS, Capability: "descriptor-relative transactional writes"}
}

func Capability() error                     { return unsupported() }
func Open(string) (*Root, error)            { return nil, unsupported() }
func (r *Root) Close() error                { return unsupported() }
func (r *Root) Lstat(string) (Entry, error) { return Entry{}, unsupported() }
func (r *Root) ReadRegular(string, int64) ([]byte, Entry, error) {
	return nil, Entry{}, unsupported()
}
func (r *Root) CreateStage(string, string) (*Stage, error) { return nil, unsupported() }
func (r *Root) CreateSymlinkStage(string, string, string) (*SymlinkStage, error) {
	return nil, unsupported()
}
func (r *Root) Detach(string, string) (*Hold, error) { return nil, unsupported() }
func (r *Root) OpenQuarantine(QuarantineRecord) (*Quarantine, error) {
	return nil, unsupported()
}
func (r *Root) SyncParent(string) error       { return unsupported() }
func (s *Stage) Write([]byte) (int, error)    { return 0, unsupported() }
func (s *Stage) Chmod(uint32) error           { return unsupported() }
func (s *Stage) Sync() error                  { return unsupported() }
func (s *Stage) Close() error                 { return unsupported() }
func (s *Stage) CommitCreate() error          { return unsupported() }
func (s *Stage) Exchange() error              { return unsupported() }
func (s *Stage) Detach(string) (*Hold, error) { return nil, unsupported() }
func (s *Stage) Remove() (*Quarantine, error) { return nil, unsupported() }
func (s *SymlinkStage) Close() error          { return unsupported() }
func (s *SymlinkStage) CommitCreate() error   { return unsupported() }
func (s *SymlinkStage) Exchange() error       { return unsupported() }
func (s *SymlinkStage) Detach(string) (*Hold, error) {
	return nil, unsupported()
}
func (s *SymlinkStage) Remove() (*Quarantine, error) { return nil, unsupported() }
func (h *Hold) Remove() (*Quarantine, error)         { return nil, unsupported() }
func (h *Hold) Close() error                         { return unsupported() }
func (q *Quarantine) Record() QuarantineRecord       { return QuarantineRecord{} }
func (q *Quarantine) Restore(string) error           { return unsupported() }
func (q *Quarantine) Close() error                   { return unsupported() }
