//go:build unix

package main

import (
	"os"
	"syscall"
)

const sessionControlSupported = true
const regularFileOpenFlags = syscall.O_NONBLOCK

func sessionOwned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid())
}
