//go:build windows

package launcher

// releaseSignals does nothing on Windows: claude is spawned and waited for by
// this process, which installs its own interrupt handling (see claude.Start).
func releaseSignals() {}
