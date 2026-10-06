//go:build windows

package claude

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// This file is UNTESTED ON REAL WINDOWS: it compiles and vets for
// windows/amd64 and windows/arm64 only.

func start(bin string, args, env []string) (int, error) {
	return spawnForeground(bin, args, env)
}

// exitCode returns the child's exit code (Windows has no signal deaths).
func exitCode(ps *os.ProcessState) int { return ps.ExitCode() }

// terminateChild delivers a termination request to the child as a console
// control event (CTRL_C_EVENT to the whole console, which includes the
// child; the launcher itself ignores it). Without a console the call fails
// and the caller's grace period ends in a kill. Unlike Process.Kill it gives
// claude the chance to restore the terminal and flush its session.
func terminateChild(*os.Process) error {
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_C_EVENT, 0)
}

// adoptChild puts the child in a job object with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE, so that the child dies when the
// launcher does, however it dies, instead of being orphaned on the console.
// It is best effort (a failure leaves the child unconfined) and there is a
// short window between the child starting and joining the job. The returned
// function closes the job handle, which kills anything still in the job.
func adoptChild(p *os.Process) func() {
	noop := func() {}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return noop
	}
	closeJob := func() { _ = windows.CloseHandle(job) }
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		closeJob()
		return noop
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.Pid))
	if err != nil {
		closeJob()
		return noop
	}
	defer func() { _ = windows.CloseHandle(h) }()
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		closeJob()
		return noop
	}
	return closeJob
}
