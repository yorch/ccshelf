//go:build windows

package gitsource

import "errors"

func mkfifo(string) error { return errors.New("no fifo on Windows") }
