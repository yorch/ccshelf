//go:build !windows

package gitsource

import "syscall"

func mkfifo(p string) error { return syscall.Mkfifo(p, 0o600) }
