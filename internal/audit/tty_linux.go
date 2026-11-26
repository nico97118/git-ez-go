//go:build linux

// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package audit

import (
	"os"
	"syscall"
	"unsafe"
)

func terminal(f *os.File) bool {
	var term syscall.Termios
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&term)))
	return e == 0
}
