package harnessfs

// EntryKind is the no-follow type of a directory entry.
type EntryKind uint8

const (
	KindUnknown EntryKind = iota
	KindRegular
	KindDirectory
	KindSymlink
	KindFIFO
	KindSocket
	KindDevice
)

func (k EntryKind) String() string {
	switch k {
	case KindRegular:
		return "regular"
	case KindDirectory:
		return "directory"
	case KindSymlink:
		return "symlink"
	case KindFIFO:
		return "fifo"
	case KindSocket:
		return "socket"
	case KindDevice:
		return "device"
	default:
		return "unknown"
	}
}

// Entry is stable metadata captured without following the leaf. Device and
// Inode let typed stage and hold handles notice name substitution before an
// operation.
type Entry struct {
	Kind   EntryKind
	Mode   uint32
	Size   int64
	Device uint64
	Inode  uint64
	Links  uint64
}

// QuarantineRecord is the journal-safe identity of a retained object. Path is
// portable and Root-relative; Entry is the descriptor-verified inode observed
// when the record was issued.
type QuarantineRecord struct {
	Path       string
	Entry      Entry
	LinkTarget string
}

func sameObject(left, right Entry) bool {
	return left.Device == right.Device && left.Inode == right.Inode && left.Kind == right.Kind
}
