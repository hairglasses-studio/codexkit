package sandbox

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"runtime"
)

type sockFilter struct {
	Code uint16
	Jt   uint8
	Jf   uint8
	K    uint32
}

const (
	bpfLd  = 0x00
	bpfW   = 0x00
	bpfAbs = 0x20
	bpfJmp = 0x05
	bpfJeq = 0x10
	bpfK   = 0x00
	bpfRet = 0x06

	auditArchX8664 = 0xc000003e

	seccompRetAllow = 0x7fff0000
	seccompRetErrno = 0x00050000

	errnoEPERM = 1

	sysPtrace        uint32 = 101
	sysPivotRoot     uint32 = 155
	sysChroot        uint32 = 161
	sysMount         uint32 = 165
	sysUmount2       uint32 = 166
	sysKexecLoad     uint32 = 246
	sysKexecFileLoad uint32 = 320
	sysMountSetattr  uint32 = 442
)

func DefaultDeniedSyscalls() []uint32 {
	return []uint32{
		sysPtrace,
		sysPivotRoot,
		sysChroot,
		sysMount,
		sysUmount2,
		sysKexecLoad,
		sysKexecFileLoad,
		sysMountSetattr,
	}
}

func BuildSeccompFilter(deniedSyscalls []uint32) ([]byte, error) {
	if runtime.GOARCH != "amd64" {
		return nil, nil
	}
	if len(deniedSyscalls) > 200 {
		return nil, fmt.Errorf("too many denied syscalls: %d", len(deniedSyscalls))
	}
	insns := []sockFilter{
		{Code: bpfLd | bpfW | bpfAbs, Jt: 0, Jf: 0, K: 4},
		{Code: bpfJmp | bpfJeq | bpfK, Jt: 1, Jf: 0, K: auditArchX8664},
		{Code: bpfRet | bpfK, Jt: 0, Jf: 0, K: seccompRetErrno | errnoEPERM},
		{Code: bpfLd | bpfW | bpfAbs, Jt: 0, Jf: 0, K: 0},
	}
	n := len(deniedSyscalls)
	for i, sc := range deniedSyscalls {
		insns = append(insns, sockFilter{
			Code: bpfJmp | bpfJeq | bpfK,
			Jt:   uint8(n - i),
			Jf:   0,
			K:    sc,
		})
	}
	insns = append(insns, sockFilter{
		Code: bpfRet | bpfK,
		Jt:   0,
		Jf:   0,
		K:    seccompRetAllow,
	})
	insns = append(insns, sockFilter{
		Code: bpfRet | bpfK,
		Jt:   0,
		Jf:   0,
		K:    seccompRetErrno | errnoEPERM,
	})

	var buf bytes.Buffer
	for _, insn := range insns {
		if err := binary.Write(&buf, binary.LittleEndian, insn); err != nil {
			return nil, fmt.Errorf("write bpf instruction: %w", err)
		}
	}
	return buf.Bytes(), nil
}
