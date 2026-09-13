package sandbox

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"sort"
)

const (
	Agent53ID   = 53
	Agent53Name = "Agent 53: Seccomp-bpf system call filter generator"
)

type SockFilter struct {
	Code uint16
	Jt   uint8
	Jf   uint8
	K    uint32
}

type sockFilter = SockFilter

type FilterMode int

const (
	ModeWhitelist FilterMode = iota
	ModeBlacklist
)

const (
	bpfLd   = 0x00
	bpfLdx  = 0x01
	bpfSt   = 0x02
	bpfStx  = 0x03
	bpfAlu  = 0x04
	bpfJmp  = 0x05
	bpfRet  = 0x06
	bpfMisc = 0x07

	bpfW = 0x00
	bpfH = 0x08
	bpfB = 0x10

	bpfImm = 0x00
	bpfAbs = 0x20
	bpfInd = 0x40
	bpfMem = 0x60
	bpfLen = 0x80
	bpfMsh = 0xa0

	bpfJa   = 0x00
	bpfJeq  = 0x10
	bpfJgt  = 0x20
	bpfJge  = 0x30
	bpfJset = 0x40

	bpfK = 0x00
	bpfX = 0x08

	auditArchX8664 = 0xc000003e

	seccompRetKillProcess = 0x80000000
	seccompRetKillThread  = 0x00000000
	seccompRetTrap        = 0x00030000
	seccompRetErrno       = 0x00050000
	seccompRetTrace       = 0x7ff00000
	seccompRetLog         = 0x7ffc0000
	seccompRetAllow       = 0x7fff0000

	errnoEPERM  = 1
	errnoEACCES = 13
	errnoENOSYS = 38
)

const (
	AuditArchX8664 = auditArchX8664

	SeccompRetKillProcess = seccompRetKillProcess
	SeccompRetKillThread  = seccompRetKillThread
	SeccompRetTrap        = seccompRetTrap
	SeccompRetErrno       = seccompRetErrno
	SeccompRetTrace       = seccompRetTrace
	SeccompRetLog         = seccompRetLog
	SeccompRetAllow       = seccompRetAllow

	ErrnoEPERM  = errnoEPERM
	ErrnoEACCES = errnoEACCES
	ErrnoENOSYS = errnoENOSYS
)

const (
	sysRead                uint32 = 0
	sysWrite               uint32 = 1
	sysOpen                uint32 = 2
	sysClose               uint32 = 3
	sysStat                uint32 = 4
	sysFstat               uint32 = 5
	sysLstat               uint32 = 6
	sysPoll                uint32 = 7
	sysLseek               uint32 = 8
	sysMmap                uint32 = 9
	sysMprotect            uint32 = 10
	sysMunmap              uint32 = 11
	sysBrk                 uint32 = 12
	sysRtSigaction         uint32 = 13
	sysRtSigprocmask       uint32 = 14
	sysRtSigreturn         uint32 = 15
	sysIoctl               uint32 = 16
	sysPread64             uint32 = 17
	sysPwrite64            uint32 = 18
	sysReadv               uint32 = 19
	sysWritev              uint32 = 20
	sysAccess              uint32 = 21
	sysPipe                uint32 = 22
	sysSelect              uint32 = 23
	sysSchedYield          uint32 = 24
	sysMremap              uint32 = 25
	sysMsync               uint32 = 26
	sysMincore             uint32 = 27
	sysMadvise             uint32 = 28
	sysDup                 uint32 = 32
	sysDup2                uint32 = 33
	sysPause               uint32 = 34
	sysNanosleep           uint32 = 35
	sysGetitimer           uint32 = 36
	sysAlarm               uint32 = 37
	sysSetitimer           uint32 = 38
	sysGetpid              uint32 = 39
	sysSendfile            uint32 = 40
	sysSocket              uint32 = 41
	sysConnect             uint32 = 42
	sysAccept              uint32 = 43
	sysSendto              uint32 = 44
	sysRecvfrom            uint32 = 45
	sysSendmsg             uint32 = 46
	sysRecvmsg             uint32 = 47
	sysShutdown            uint32 = 48
	sysBind                uint32 = 49
	sysListen              uint32 = 50
	sysGetsockname         uint32 = 51
	sysGetpeername         uint32 = 52
	sysSocketpair          uint32 = 53
	sysSetsockopt          uint32 = 54
	sysGetsockopt          uint32 = 55
	sysClone               uint32 = 56
	sysFork                uint32 = 57
	sysVfork               uint32 = 58
	sysExecve              uint32 = 59
	sysExit                uint32 = 60
	sysWait4               uint32 = 61
	sysKill                uint32 = 62
	sysUname               uint32 = 63
	sysFcntl               uint32 = 72
	sysFlock               uint32 = 73
	sysFsync               uint32 = 74
	sysFdatasync           uint32 = 75
	sysTruncate            uint32 = 76
	sysFtruncate           uint32 = 77
	sysGetdents            uint32 = 78
	sysGetcwd              uint32 = 79
	sysChdir               uint32 = 80
	sysFchdir              uint32 = 81
	sysRename              uint32 = 82
	sysMkdir               uint32 = 83
	sysRmdir               uint32 = 84
	sysCreat               uint32 = 85
	sysLink                uint32 = 86
	sysUnlink              uint32 = 87
	sysSymlink             uint32 = 88
	sysReadlink            uint32 = 89
	sysChmod               uint32 = 90
	sysFchmod              uint32 = 91
	sysChown               uint32 = 92
	sysFchown              uint32 = 93
	sysLchown              uint32 = 94
	sysUmask               uint32 = 95
	sysGettimeofday        uint32 = 96
	sysGetrlimit           uint32 = 97
	sysGetrusage           uint32 = 98
	sysSysinfo             uint32 = 99
	sysTimes               uint32 = 100
	sysPtrace              uint32 = 101
	sysGetuid              uint32 = 102
	sysSyslog              uint32 = 103
	sysGetgid              uint32 = 104
	sysSetuid              uint32 = 105
	sysSetgid              uint32 = 106
	sysGeteuid             uint32 = 107
	sysGetegid             uint32 = 108
	sysSetpgid             uint32 = 109
	sysGetppid             uint32 = 110
	sysGetpgrp             uint32 = 111
	sysSetsid              uint32 = 112
	sysSetreuid            uint32 = 113
	sysSetregid            uint32 = 114
	sysGetgroups           uint32 = 115
	sysSetgroups           uint32 = 116
	sysSetresuid           uint32 = 117
	sysGetresuid           uint32 = 118
	sysSetresgid           uint32 = 119
	sysGetresgid           uint32 = 120
	sysGetpgid             uint32 = 121
	sysGetsid              uint32 = 124
	sysRtSigpending        uint32 = 127
	sysRtSigtimedwait      uint32 = 128
	sysRtSigqueueinfo      uint32 = 129
	sysRtSigsuspend        uint32 = 130
	sysSigaltstack         uint32 = 131
	sysUtime               uint32 = 132
	sysStatfs              uint32 = 137
	sysFstatfs             uint32 = 138
	sysSchedSetparam       uint32 = 142
	sysSchedGetparam       uint32 = 143
	sysSchedSetscheduler   uint32 = 144
	sysSchedGetscheduler   uint32 = 145
	sysSchedGetPriorityMax uint32 = 146
	sysSchedGetPriorityMin uint32 = 147
	sysSchedRrGetInterval  uint32 = 148
	sysMlock               uint32 = 149
	sysMunlock             uint32 = 150
	sysMlockall            uint32 = 151
	sysMunlockall          uint32 = 152
	sysPivotRoot           uint32 = 155
	sysArchPrctl           uint32 = 158
	sysChroot              uint32 = 161
	sysMount               uint32 = 165
	sysUmount2             uint32 = 166
	sysGettid              uint32 = 186
	sysFutex               uint32 = 202
	sysSchedSetaffinity    uint32 = 203
	sysSchedGetaffinity    uint32 = 204
	sysSetThreadArea       uint32 = 205
	sysGetThreadArea       uint32 = 211
	sysEpollCreate         uint32 = 213
	sysGetdents64          uint32 = 217
	sysSetTidAddress       uint32 = 218
	sysRestartSyscall      uint32 = 219
	sysFadvise64           uint32 = 221
	sysTimerCreate         uint32 = 222
	sysTimerSettime        uint32 = 223
	sysTimerGettime        uint32 = 224
	sysTimerGetoverrun     uint32 = 225
	sysTimerDelete         uint32 = 226
	sysClockSettime        uint32 = 227
	sysClockGettime        uint32 = 228
	sysClockGetres         uint32 = 229
	sysClockNanosleep      uint32 = 230
	sysExitGroup           uint32 = 231
	sysEpollWait           uint32 = 232
	sysEpollCtl            uint32 = 233
	sysTgkill              uint32 = 234
	sysUtimes              uint32 = 235
	sysKexecLoad           uint32 = 246
	sysWaitid              uint32 = 247
	sysOpenat              uint32 = 257
	sysMkdirat             uint32 = 258
	sysMknodat             uint32 = 259
	sysFchownat            uint32 = 260
	sysFutimesat           uint32 = 261
	sysNewfstatat          uint32 = 262
	sysUnlinkat            uint32 = 263
	sysRenameat            uint32 = 264
	sysLinkat              uint32 = 265
	sysSymlinkat           uint32 = 266
	sysReadlinkat          uint32 = 267
	sysFchmodat            uint32 = 268
	sysFaccessat           uint32 = 269
	sysPselect6            uint32 = 270
	sysPpoll               uint32 = 271
	sysSetRobustList       uint32 = 273
	sysGetRobustList       uint32 = 274
	sysSplice              uint32 = 275
	sysTee                 uint32 = 276
	sysSyncFileRange       uint32 = 277
	sysVmsplice            uint32 = 278
	sysUtimensat           uint32 = 280
	sysEpollPwait          uint32 = 281
	sysTimerfdCreate       uint32 = 283
	sysTimerfdSettime      uint32 = 286
	sysTimerfdGettime      uint32 = 287
	sysAccept4             uint32 = 288
	sysEventfd             uint32 = 290
	sysEpollCreate1        uint32 = 291
	sysDup3                uint32 = 292
	sysPipe2               uint32 = 293
	sysInotifyInit1        uint32 = 294
	sysPreadv              uint32 = 295
	sysPwritev             uint32 = 296
	sysRtTgsigqueueinfo    uint32 = 297
	sysRecvmmsg            uint32 = 299
	sysPrlimit64           uint32 = 302
	sysClockAdjtime        uint32 = 305
	sysSyncfs              uint32 = 306
	sysSendmmsg            uint32 = 307
	sysGetcpu              uint32 = 309
	sysGetrandom           uint32 = 318
	sysMemfdCreate         uint32 = 319
	sysKexecFileLoad       uint32 = 320
	sysExecveat            uint32 = 322
	sysCopyFileRange       uint32 = 326
	sysPreadv2             uint32 = 327
	sysPwritev2            uint32 = 328
	sysStatx               uint32 = 332
	sysRseq                uint32 = 334
	sysPidfdSendSignal     uint32 = 424
	sysPidfdOpen           uint32 = 434
	sysClone3              uint32 = 435
	sysCloseRange          uint32 = 436
	sysOpenat2             uint32 = 437
	sysFaccessat2          uint32 = 439
	sysEpollPwait2         uint32 = 441
	sysMountSetattr        uint32 = 442
	sysFutexWaitv          uint32 = 449
)

const (
	SysRead                = sysRead
	SysWrite               = sysWrite
	SysOpen                = sysOpen
	SysClose               = sysClose
	SysStat                = sysStat
	SysFstat               = sysFstat
	SysLstat               = sysLstat
	SysPoll                = sysPoll
	SysLseek               = sysLseek
	SysMmap                = sysMmap
	SysMprotect            = sysMprotect
	SysMunmap              = sysMunmap
	SysBrk                 = sysBrk
	SysRtSigaction         = sysRtSigaction
	SysRtSigprocmask       = sysRtSigprocmask
	SysRtSigreturn         = sysRtSigreturn
	SysIoctl               = sysIoctl
	SysPread64             = sysPread64
	SysPwrite64            = sysPwrite64
	SysReadv               = sysReadv
	SysWritev              = sysWritev
	SysAccess              = sysAccess
	SysPipe                = sysPipe
	SysSelect              = sysSelect
	SysSchedYield          = sysSchedYield
	SysMremap              = sysMremap
	SysMsync               = sysMsync
	SysMincore             = sysMincore
	SysMadvise             = sysMadvise
	SysDup                 = sysDup
	SysDup2                = sysDup2
	SysPause               = sysPause
	SysNanosleep           = sysNanosleep
	SysGetitimer           = sysGetitimer
	SysAlarm               = sysAlarm
	SysSetitimer           = sysSetitimer
	SysGetpid              = sysGetpid
	SysSendfile            = sysSendfile
	SysSocket              = sysSocket
	SysConnect             = sysConnect
	SysAccept              = sysAccept
	SysSendto              = sysSendto
	SysRecvfrom            = sysRecvfrom
	SysSendmsg             = sysSendmsg
	SysRecvmsg             = sysRecvmsg
	SysShutdown            = sysShutdown
	SysBind                = sysBind
	SysListen              = sysListen
	SysGetsockname         = sysGetsockname
	SysGetpeername         = sysGetpeername
	SysSocketpair          = sysSocketpair
	SysSetsockopt          = sysSetsockopt
	SysGetsockopt          = sysGetsockopt
	SysClone               = sysClone
	SysFork                = sysFork
	SysVfork               = sysVfork
	SysExecve              = sysExecve
	SysExit                = sysExit
	SysWait4               = sysWait4
	SysKill                = sysKill
	SysUname               = sysUname
	SysFcntl               = sysFcntl
	SysFlock               = sysFlock
	SysFsync               = sysFsync
	SysFdatasync           = sysFdatasync
	SysTruncate            = sysTruncate
	SysFtruncate           = sysFtruncate
	SysGetdents            = sysGetdents
	SysGetcwd              = sysGetcwd
	SysChdir               = sysChdir
	SysFchdir              = sysFchdir
	SysRename              = sysRename
	SysMkdir               = sysMkdir
	SysRmdir               = sysRmdir
	SysCreat               = sysCreat
	SysLink                = sysLink
	SysUnlink              = sysUnlink
	SysSymlink             = sysSymlink
	SysReadlink            = sysReadlink
	SysChmod               = sysChmod
	SysFchmod              = sysFchmod
	SysChown               = sysChown
	SysFchown              = sysFchown
	SysLchown              = sysLchown
	SysUmask               = sysUmask
	SysGettimeofday        = sysGettimeofday
	SysGetrlimit           = sysGetrlimit
	SysGetrusage           = sysGetrusage
	SysSysinfo             = sysSysinfo
	SysTimes               = sysTimes
	SysPtrace              = sysPtrace
	SysGetuid              = sysGetuid
	SysSyslog              = sysSyslog
	SysGetgid              = sysGetgid
	SysSetuid              = sysSetuid
	SysSetgid              = sysSetgid
	SysGeteuid             = sysGeteuid
	SysGetegid             = sysGetegid
	SysSetpgid             = sysSetpgid
	SysGetppid             = sysGetppid
	SysGetpgrp             = sysGetpgrp
	SysSetsid              = sysSetsid
	SysSetreuid            = sysSetreuid
	SysSetregid            = sysSetregid
	SysGetgroups           = sysGetgroups
	SysSetgroups           = sysSetgroups
	SysSetresuid           = sysSetresuid
	SysGetresuid           = sysGetresuid
	SysSetresgid           = sysSetresgid
	SysGetresgid           = sysGetresgid
	SysGetpgid             = sysGetpgid
	SysGetsid              = sysGetsid
	SysRtSigpending        = sysRtSigpending
	SysRtSigtimedwait      = sysRtSigtimedwait
	SysRtSigqueueinfo      = sysRtSigqueueinfo
	SysRtSigsuspend        = sysRtSigsuspend
	SysSigaltstack         = sysSigaltstack
	SysUtime               = sysUtime
	SysStatfs              = sysStatfs
	SysFstatfs             = sysFstatfs
	SysSchedSetparam       = sysSchedSetparam
	SysSchedGetparam       = sysSchedGetparam
	SysSchedSetscheduler   = sysSchedSetscheduler
	SysSchedGetscheduler   = sysSchedGetscheduler
	SysSchedGetPriorityMax = sysSchedGetPriorityMax
	SysSchedGetPriorityMin = sysSchedGetPriorityMin
	SysSchedRrGetInterval  = sysSchedRrGetInterval
	SysMlock               = sysMlock
	SysMunlock             = sysMunlock
	SysMlockall            = sysMlockall
	SysMunlockall          = sysMunlockall
	SysPivotRoot           = sysPivotRoot
	SysArchPrctl           = sysArchPrctl
	SysChroot              = sysChroot
	SysMount               = sysMount
	SysUmount2             = sysUmount2
	SysGettid              = sysGettid
	SysFutex               = sysFutex
	SysSchedSetaffinity    = sysSchedSetaffinity
	SysSchedGetaffinity    = sysSchedGetaffinity
	SysSetThreadArea       = sysSetThreadArea
	SysGetThreadArea       = sysGetThreadArea
	SysEpollCreate         = sysEpollCreate
	SysGetdents64          = sysGetdents64
	SysSetTidAddress       = sysSetTidAddress
	SysRestartSyscall      = sysRestartSyscall
	SysFadvise64           = sysFadvise64
	SysTimerCreate         = sysTimerCreate
	SysTimerSettime        = sysTimerSettime
	SysTimerGettime        = sysTimerGettime
	SysTimerGetoverrun     = sysTimerGetoverrun
	SysTimerDelete         = sysTimerDelete
	SysClockSettime        = sysClockSettime
	SysClockGettime        = sysClockGettime
	SysClockGetres         = sysClockGetres
	SysClockNanosleep      = sysClockNanosleep
	SysExitGroup           = sysExitGroup
	SysEpollWait           = sysEpollWait
	SysEpollCtl            = sysEpollCtl
	SysTgkill              = sysTgkill
	SysUtimes              = sysUtimes
	SysKexecLoad           = sysKexecLoad
	SysWaitid              = sysWaitid
	SysOpenat              = sysOpenat
	SysMkdirat             = sysMkdirat
	SysMknodat             = sysMknodat
	SysFchownat            = sysFchownat
	SysFutimesat           = sysFutimesat
	SysNewfstatat          = sysNewfstatat
	SysUnlinkat            = sysUnlinkat
	SysRenameat            = sysRenameat
	SysLinkat              = sysLinkat
	SysSymlinkat           = sysSymlinkat
	SysReadlinkat          = sysReadlinkat
	SysFchmodat            = sysFchmodat
	SysFaccessat           = sysFaccessat
	SysPselect6            = sysPselect6
	SysPpoll               = sysPpoll
	SysSetRobustList       = sysSetRobustList
	SysGetRobustList       = sysGetRobustList
	SysSplice              = sysSplice
	SysTee                 = sysTee
	SysSyncFileRange       = sysSyncFileRange
	SysVmsplice            = sysVmsplice
	SysUtimensat           = sysUtimensat
	SysEpollPwait          = sysEpollPwait
	SysTimerfdCreate       = sysTimerfdCreate
	SysTimerfdSettime      = sysTimerfdSettime
	SysTimerfdGettime      = sysTimerfdGettime
	SysAccept4             = sysAccept4
	SysEventfd             = sysEventfd
	SysEpollCreate1        = sysEpollCreate1
	SysDup3                = sysDup3
	SysPipe2               = sysPipe2
	SysInotifyInit1        = sysInotifyInit1
	SysPreadv              = sysPreadv
	SysPwritev             = sysPwritev
	SysRtTgsigqueueinfo    = sysRtTgsigqueueinfo
	SysRecvmmsg            = sysRecvmmsg
	SysPrlimit64           = sysPrlimit64
	SysClockAdjtime        = sysClockAdjtime
	SysSyncfs              = sysSyncfs
	SysSendmmsg            = sysSendmmsg
	SysGetcpu              = sysGetcpu
	SysGetrandom           = sysGetrandom
	SysMemfdCreate         = sysMemfdCreate
	SysKexecFileLoad       = sysKexecFileLoad
	SysExecveat            = sysExecveat
	SysCopyFileRange       = sysCopyFileRange
	SysPreadv2             = sysPreadv2
	SysPwritev2            = sysPwritev2
	SysStatx               = sysStatx
	SysRseq                = sysRseq
	SysPidfdSendSignal     = sysPidfdSendSignal
	SysPidfdOpen           = sysPidfdOpen
	SysClone3              = sysClone3
	SysCloseRange          = sysCloseRange
	SysOpenat2             = sysOpenat2
	SysFaccessat2          = sysFaccessat2
	SysEpollPwait2         = sysEpollPwait2
	SysMountSetattr        = sysMountSetattr
	SysFutexWaitv          = sysFutexWaitv
)

var syscallNames = map[uint32]string{
	sysRead:                "read",
	sysWrite:               "write",
	sysOpen:                "open",
	sysClose:               "close",
	sysStat:                "stat",
	sysFstat:               "fstat",
	sysLstat:               "lstat",
	sysPoll:                "poll",
	sysLseek:               "lseek",
	sysMmap:                "mmap",
	sysMprotect:            "mprotect",
	sysMunmap:              "munmap",
	sysBrk:                 "brk",
	sysRtSigaction:         "rt_sigaction",
	sysRtSigprocmask:       "rt_sigprocmask",
	sysRtSigreturn:         "rt_sigreturn",
	sysIoctl:               "ioctl",
	sysPread64:             "pread64",
	sysPwrite64:            "pwrite64",
	sysReadv:               "readv",
	sysWritev:              "writev",
	sysAccess:              "access",
	sysPipe:                "pipe",
	sysSelect:              "select",
	sysSchedYield:          "sched_yield",
	sysMremap:              "mremap",
	sysMsync:               "msync",
	sysMincore:             "mincore",
	sysMadvise:             "madvise",
	sysDup:                 "dup",
	sysDup2:                "dup2",
	sysPause:               "pause",
	sysNanosleep:           "nanosleep",
	sysGetitimer:           "getitimer",
	sysAlarm:               "alarm",
	sysSetitimer:           "setitimer",
	sysGetpid:              "getpid",
	sysSendfile:            "sendfile",
	sysSocket:              "socket",
	sysConnect:             "connect",
	sysAccept:              "accept",
	sysSendto:              "sendto",
	sysRecvfrom:            "recvfrom",
	sysSendmsg:             "sendmsg",
	sysRecvmsg:             "recvmsg",
	sysShutdown:            "shutdown",
	sysBind:                "bind",
	sysListen:              "listen",
	sysGetsockname:         "getsockname",
	sysGetpeername:         "getpeername",
	sysSocketpair:          "socketpair",
	sysSetsockopt:          "setsockopt",
	sysGetsockopt:          "getsockopt",
	sysClone:               "clone",
	sysFork:                "fork",
	sysVfork:               "vfork",
	sysExecve:              "execve",
	sysExit:                "exit",
	sysWait4:               "wait4",
	sysKill:                "kill",
	sysUname:               "uname",
	sysFcntl:               "fcntl",
	sysFlock:               "flock",
	sysFsync:               "fsync",
	sysFdatasync:           "fdatasync",
	sysTruncate:            "truncate",
	sysFtruncate:           "ftruncate",
	sysGetdents:            "getdents",
	sysGetcwd:              "getcwd",
	sysChdir:               "chdir",
	sysFchdir:              "fchdir",
	sysRename:              "rename",
	sysMkdir:               "mkdir",
	sysRmdir:               "rmdir",
	sysCreat:               "creat",
	sysLink:                "link",
	sysUnlink:              "unlink",
	sysSymlink:             "symlink",
	sysReadlink:            "readlink",
	sysChmod:               "chmod",
	sysFchmod:              "fchmod",
	sysChown:               "chown",
	sysFchown:              "fchown",
	sysLchown:              "lchown",
	sysUmask:               "umask",
	sysGettimeofday:        "gettimeofday",
	sysGetrlimit:           "getrlimit",
	sysGetrusage:           "getrusage",
	sysSysinfo:             "sysinfo",
	sysTimes:               "times",
	sysPtrace:              "ptrace",
	sysGetuid:              "getuid",
	sysSyslog:              "syslog",
	sysGetgid:              "getgid",
	sysSetuid:              "setuid",
	sysSetgid:              "setgid",
	sysGeteuid:             "geteuid",
	sysGetegid:             "getegid",
	sysSetpgid:             "setpgid",
	sysGetppid:             "getppid",
	sysGetpgrp:             "getpgrp",
	sysSetsid:              "setsid",
	sysSetreuid:            "setreuid",
	sysSetregid:            "setregid",
	sysGetgroups:           "getgroups",
	sysSetgroups:           "setgroups",
	sysSetresuid:           "setresuid",
	sysGetresuid:           "getresuid",
	sysSetresgid:           "setresgid",
	sysGetresgid:           "getresgid",
	sysGetpgid:             "getpgid",
	sysGetsid:              "getsid",
	sysRtSigpending:        "rt_sigpending",
	sysRtSigtimedwait:      "rt_sigtimedwait",
	sysRtSigqueueinfo:      "rt_sigqueueinfo",
	sysRtSigsuspend:        "rt_sigsuspend",
	sysSigaltstack:         "sigaltstack",
	sysUtime:               "utime",
	sysStatfs:              "statfs",
	sysFstatfs:             "fstatfs",
	sysSchedSetparam:       "sched_setparam",
	sysSchedGetparam:       "sched_getparam",
	sysSchedSetscheduler:   "sched_setscheduler",
	sysSchedGetscheduler:   "sched_getscheduler",
	sysSchedGetPriorityMax: "sched_get_priority_max",
	sysSchedGetPriorityMin: "sched_get_priority_min",
	sysSchedRrGetInterval:  "sched_rr_get_interval",
	sysMlock:               "mlock",
	sysMunlock:             "munlock",
	sysMlockall:            "mlockall",
	sysMunlockall:          "munlockall",
	sysPivotRoot:           "pivot_root",
	sysArchPrctl:           "arch_prctl",
	sysChroot:              "chroot",
	sysMount:               "mount",
	sysUmount2:             "umount2",
	sysGettid:              "gettid",
	sysFutex:               "futex",
	sysSchedSetaffinity:    "sched_setaffinity",
	sysSchedGetaffinity:    "sched_getaffinity",
	sysSetThreadArea:       "set_thread_area",
	sysGetThreadArea:       "get_thread_area",
	sysEpollCreate:         "epoll_create",
	sysGetdents64:          "getdents64",
	sysSetTidAddress:       "set_tid_address",
	sysRestartSyscall:      "restart_syscall",
	sysFadvise64:           "fadvise64",
	sysTimerCreate:         "timer_create",
	sysTimerSettime:        "timer_settime",
	sysTimerGettime:        "timer_gettime",
	sysTimerGetoverrun:     "timer_getoverrun",
	sysTimerDelete:         "timer_delete",
	sysClockSettime:        "clock_settime",
	sysClockGettime:        "clock_gettime",
	sysClockGetres:         "clock_getres",
	sysClockNanosleep:      "clock_nanosleep",
	sysExitGroup:           "exit_group",
	sysEpollWait:           "epoll_wait",
	sysEpollCtl:            "epoll_ctl",
	sysTgkill:              "tgkill",
	sysUtimes:              "utimes",
	sysKexecLoad:           "kexec_load",
	sysWaitid:              "waitid",
	sysOpenat:              "openat",
	sysMkdirat:             "mkdirat",
	sysMknodat:             "mknodat",
	sysFchownat:            "fchownat",
	sysFutimesat:           "futimesat",
	sysNewfstatat:          "newfstatat",
	sysUnlinkat:            "unlinkat",
	sysRenameat:            "renameat",
	sysLinkat:              "linkat",
	sysSymlinkat:           "symlinkat",
	sysReadlinkat:          "readlinkat",
	sysFchmodat:            "fchmodat",
	sysFaccessat:           "faccessat",
	sysPselect6:            "pselect6",
	sysPpoll:               "ppoll",
	sysSetRobustList:       "set_robust_list",
	sysGetRobustList:       "get_robust_list",
	sysSplice:              "splice",
	sysTee:                 "tee",
	sysSyncFileRange:       "sync_file_range",
	sysVmsplice:            "vmsplice",
	sysUtimensat:           "utimensat",
	sysEpollPwait:          "epoll_pwait",
	sysTimerfdCreate:       "timerfd_create",
	sysTimerfdSettime:      "timerfd_settime",
	sysTimerfdGettime:      "timerfd_gettime",
	sysAccept4:             "accept4",
	sysEventfd:             "eventfd",
	sysEpollCreate1:        "epoll_create1",
	sysDup3:                "dup3",
	sysPipe2:               "pipe2",
	sysInotifyInit1:        "inotify_init1",
	sysPreadv:              "preadv",
	sysPwritev:             "pwritev",
	sysRtTgsigqueueinfo:    "rt_tgsigqueueinfo",
	sysRecvmmsg:            "recvmmsg",
	sysPrlimit64:           "prlimit64",
	sysClockAdjtime:        "clock_adjtime",
	sysSyncfs:              "syncfs",
	sysSendmmsg:            "sendmmsg",
	sysGetcpu:              "getcpu",
	sysGetrandom:           "getrandom",
	sysMemfdCreate:         "memfd_create",
	sysKexecFileLoad:       "kexec_file_load",
	sysExecveat:            "execveat",
	sysCopyFileRange:       "copy_file_range",
	sysPreadv2:             "preadv2",
	sysPwritev2:            "pwritev2",
	sysStatx:               "statx",
	sysRseq:                "rseq",
	sysPidfdSendSignal:     "pidfd_send_signal",
	sysPidfdOpen:           "pidfd_open",
	sysClone3:              "clone3",
	sysCloseRange:          "close_range",
	sysOpenat2:             "openat2",
	sysFaccessat2:          "faccessat2",
	sysEpollPwait2:         "epoll_pwait2",
	sysMountSetattr:        "mount_setattr",
	sysFutexWaitv:          "futex_waitv",
}

var syscallNumbers = func() map[string]uint32 {
	m := make(map[string]uint32, len(syscallNames))
	for nr, name := range syscallNames {
		m[name] = nr
	}
	return m
}()

func SyscallName(nr uint32) string {
	if name, ok := syscallNames[nr]; ok {
		return name
	}
	return fmt.Sprintf("syscall_%d", nr)
}

func SyscallNumber(name string) (uint32, bool) {
	nr, ok := syscallNumbers[name]
	return nr, ok
}

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

func DefaultAllowedSyscalls() []uint32 {
	return []uint32{
		sysRead,
		sysWrite,
		sysOpen,
		sysClose,
		sysStat,
		sysFstat,
		sysLstat,
		sysPoll,
		sysLseek,
		sysMmap,
		sysMprotect,
		sysMunmap,
		sysBrk,
		sysRtSigaction,
		sysRtSigprocmask,
		sysRtSigreturn,
		sysIoctl,
		sysPread64,
		sysPwrite64,
		sysReadv,
		sysWritev,
		sysAccess,
		sysPipe,
		sysSelect,
		sysSchedYield,
		sysMremap,
		sysMsync,
		sysMincore,
		sysMadvise,
		sysDup,
		sysDup2,
		sysPause,
		sysNanosleep,
		sysGetitimer,
		sysAlarm,
		sysSetitimer,
		sysGetpid,
		sysSendfile,
		sysSocket,
		sysConnect,
		sysAccept,
		sysSendto,
		sysRecvfrom,
		sysSendmsg,
		sysRecvmsg,
		sysShutdown,
		sysBind,
		sysListen,
		sysGetsockname,
		sysGetpeername,
		sysSocketpair,
		sysSetsockopt,
		sysGetsockopt,
		sysClone,
		sysFork,
		sysVfork,
		sysExecve,
		sysExit,
		sysWait4,
		sysKill,
		sysUname,
		sysFcntl,
		sysFlock,
		sysFsync,
		sysFdatasync,
		sysTruncate,
		sysFtruncate,
		sysGetdents,
		sysGetcwd,
		sysChdir,
		sysFchdir,
		sysRename,
		sysMkdir,
		sysRmdir,
		sysCreat,
		sysLink,
		sysUnlink,
		sysSymlink,
		sysReadlink,
		sysChmod,
		sysFchmod,
		sysChown,
		sysFchown,
		sysLchown,
		sysUmask,
		sysGettimeofday,
		sysGetrlimit,
		sysGetrusage,
		sysSysinfo,
		sysTimes,
		sysGetuid,
		sysSyslog,
		sysGetgid,
		sysSetuid,
		sysSetgid,
		sysGeteuid,
		sysGetegid,
		sysSetpgid,
		sysGetppid,
		sysGetpgrp,
		sysSetsid,
		sysSetreuid,
		sysSetregid,
		sysGetgroups,
		sysSetgroups,
		sysSetresuid,
		sysGetresuid,
		sysSetresgid,
		sysGetresgid,
		sysGetpgid,
		sysGetsid,
		sysRtSigpending,
		sysRtSigtimedwait,
		sysRtSigqueueinfo,
		sysRtSigsuspend,
		sysSigaltstack,
		sysUtime,
		sysStatfs,
		sysFstatfs,
		sysSchedSetparam,
		sysSchedGetparam,
		sysSchedSetscheduler,
		sysSchedGetscheduler,
		sysSchedGetPriorityMax,
		sysSchedGetPriorityMin,
		sysSchedRrGetInterval,
		sysMlock,
		sysMunlock,
		sysMlockall,
		sysMunlockall,
		sysArchPrctl,
		sysGettid,
		sysFutex,
		sysSchedSetaffinity,
		sysSchedGetaffinity,
		sysSetThreadArea,
		sysGetThreadArea,
		sysEpollCreate,
		sysGetdents64,
		sysSetTidAddress,
		sysRestartSyscall,
		sysFadvise64,
		sysTimerCreate,
		sysTimerSettime,
		sysTimerGettime,
		sysTimerGetoverrun,
		sysTimerDelete,
		sysClockSettime,
		sysClockGettime,
		sysClockGetres,
		sysClockNanosleep,
		sysExitGroup,
		sysEpollWait,
		sysEpollCtl,
		sysTgkill,
		sysUtimes,
		sysWaitid,
		sysOpenat,
		sysMkdirat,
		sysMknodat,
		sysFchownat,
		sysFutimesat,
		sysNewfstatat,
		sysUnlinkat,
		sysRenameat,
		sysLinkat,
		sysSymlinkat,
		sysReadlinkat,
		sysFchmodat,
		sysFaccessat,
		sysPselect6,
		sysPpoll,
		sysSetRobustList,
		sysGetRobustList,
		sysSplice,
		sysTee,
		sysSyncFileRange,
		sysVmsplice,
		sysUtimensat,
		sysEpollPwait,
		sysTimerfdCreate,
		sysTimerfdSettime,
		sysTimerfdGettime,
		sysAccept4,
		sysEventfd,
		sysEpollCreate1,
		sysDup3,
		sysPipe2,
		sysInotifyInit1,
		sysPreadv,
		sysPwritev,
		sysRtTgsigqueueinfo,
		sysRecvmmsg,
		sysPrlimit64,
		sysClockAdjtime,
		sysSyncfs,
		sysSendmmsg,
		sysGetcpu,
		sysGetrandom,
		sysMemfdCreate,
		sysExecveat,
		sysCopyFileRange,
		sysPreadv2,
		sysPwritev2,
		sysStatx,
		sysRseq,
		sysPidfdSendSignal,
		sysPidfdOpen,
		sysClone3,
		sysCloseRange,
		sysOpenat2,
		sysFaccessat2,
		sysEpollPwait2,
		sysFutexWaitv,
	}
}

func DefaultAllowedSyscallNames() []string {
	allowed := DefaultAllowedSyscalls()
	names := make([]string, 0, len(allowed))
	for _, sc := range allowed {
		names = append(names, SyscallName(sc))
	}
	return names
}

type FilterGenerator struct {
	mode          FilterMode
	arch          uint32
	defaultAction uint32
	badArchAction uint32
	allowed       map[uint32]bool
	denied        map[uint32]uint32
}

func NewFilterGenerator() *FilterGenerator {
	return &FilterGenerator{
		mode:          ModeWhitelist,
		arch:          auditArchX8664,
		defaultAction: seccompRetErrno | errnoEPERM,
		badArchAction: seccompRetKillProcess,
		allowed:       make(map[uint32]bool),
		denied:        make(map[uint32]uint32),
	}
}

func NewWhitelistGenerator() *FilterGenerator {
	return NewFilterGenerator()
}

func NewBlacklistGenerator() *FilterGenerator {
	g := NewFilterGenerator()
	g.mode = ModeBlacklist
	g.defaultAction = seccompRetAllow
	return g
}

func (g *FilterGenerator) SetMode(mode FilterMode) *FilterGenerator {
	g.mode = mode
	return g
}

func (g *FilterGenerator) SetArch(arch uint32) *FilterGenerator {
	g.arch = arch
	return g
}

func (g *FilterGenerator) SetDefaultAction(action uint32) *FilterGenerator {
	g.defaultAction = action
	return g
}

func (g *FilterGenerator) SetBadArchAction(action uint32) *FilterGenerator {
	g.badArchAction = action
	return g
}

func (g *FilterGenerator) Allow(syscalls ...uint32) *FilterGenerator {
	for _, sc := range syscalls {
		g.allowed[sc] = true
		delete(g.denied, sc)
	}
	return g
}

func (g *FilterGenerator) AllowByName(names ...string) error {
	for _, name := range names {
		nr, ok := SyscallNumber(name)
		if !ok {
			return fmt.Errorf("unknown syscall name: %s", name)
		}
		g.Allow(nr)
	}
	return nil
}

func (g *FilterGenerator) Deny(action uint32, syscalls ...uint32) *FilterGenerator {
	for _, sc := range syscalls {
		g.denied[sc] = action
		delete(g.allowed, sc)
	}
	return g
}

func (g *FilterGenerator) DenyByName(action uint32, names ...string) error {
	for _, name := range names {
		nr, ok := SyscallNumber(name)
		if !ok {
			return fmt.Errorf("unknown syscall name: %s", name)
		}
		g.Deny(action, nr)
	}
	return nil
}

func (g *FilterGenerator) Remove(syscalls ...uint32) *FilterGenerator {
	for _, sc := range syscalls {
		delete(g.allowed, sc)
		delete(g.denied, sc)
	}
	return g
}

func (g *FilterGenerator) AllowedSyscalls() []uint32 {
	res := make([]uint32, 0, len(g.allowed))
	for sc := range g.allowed {
		res = append(res, sc)
	}
	sort.Slice(res, func(i, j int) bool { return res[i] < res[j] })
	return res
}

func (g *FilterGenerator) DeniedSyscalls() map[uint32]uint32 {
	res := make(map[uint32]uint32, len(g.denied))
	for sc, act := range g.denied {
		res[sc] = act
	}
	return res
}

func (g *FilterGenerator) BuildInstructions() ([]SockFilter, error) {
	if runtime.GOARCH != "amd64" {
		return nil, nil
	}

	if g.mode == ModeWhitelist && len(g.allowed) == 0 && len(g.denied) == 0 {
		return nil, errors.New("whitelist filter generator has no allowed syscalls")
	}

	insns := []SockFilter{
		{Code: bpfLd | bpfW | bpfAbs, Jt: 0, Jf: 0, K: 4},
		{Code: bpfJmp | bpfJeq | bpfK, Jt: 1, Jf: 0, K: g.arch},
		{Code: bpfRet | bpfK, Jt: 0, Jf: 0, K: g.badArchAction},
		{Code: bpfLd | bpfW | bpfAbs, Jt: 0, Jf: 0, K: 0},
	}

	var deniedList []uint32
	for sc := range g.denied {
		deniedList = append(deniedList, sc)
	}
	sort.Slice(deniedList, func(i, j int) bool { return deniedList[i] < deniedList[j] })
	for _, sc := range deniedList {
		act := g.denied[sc]
		insns = append(insns,
			SockFilter{Code: bpfJmp | bpfJeq | bpfK, Jt: 0, Jf: 1, K: sc},
			SockFilter{Code: bpfRet | bpfK, Jt: 0, Jf: 0, K: act},
		)
	}

	if g.mode == ModeWhitelist {
		var allowedList []uint32
		for sc := range g.allowed {
			allowedList = append(allowedList, sc)
		}
		sort.Slice(allowedList, func(i, j int) bool { return allowedList[i] < allowedList[j] })
		for _, sc := range allowedList {
			insns = append(insns,
				SockFilter{Code: bpfJmp | bpfJeq | bpfK, Jt: 0, Jf: 1, K: sc},
				SockFilter{Code: bpfRet | bpfK, Jt: 0, Jf: 0, K: seccompRetAllow},
			)
		}
	}

	insns = append(insns, SockFilter{
		Code: bpfRet | bpfK,
		Jt:   0,
		Jf:   0,
		K:    g.defaultAction,
	})

	return insns, nil
}

func (g *FilterGenerator) Build() ([]byte, error) {
	insns, err := g.BuildInstructions()
	if err != nil {
		return nil, err
	}
	if insns == nil {
		return nil, nil
	}

	var buf bytes.Buffer
	for _, insn := range insns {
		if err := binary.Write(&buf, binary.LittleEndian, insn); err != nil {
			return nil, fmt.Errorf("write bpf instruction: %w", err)
		}
	}
	return buf.Bytes(), nil
}

func BuildWhitelistFilter(allowedSyscalls []uint32) ([]byte, error) {
	if runtime.GOARCH != "amd64" {
		return nil, nil
	}
	gen := NewFilterGenerator()
	gen.SetDefaultAction(seccompRetErrno | errnoEPERM)
	gen.Allow(allowedSyscalls...)
	return gen.Build()
}

func BuildDefaultWhitelistFilter() ([]byte, error) {
	return BuildWhitelistFilter(DefaultAllowedSyscalls())
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
