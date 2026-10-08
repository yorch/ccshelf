// Package e2e holds end-to-end tests of the real ccshelf binary.
//
// The tests build cmd/ccshelf and the fake claude (internal/testutil/fakeclaude)
// once in TestMain, then run the binary as a subprocess in a fully isolated
// environment: a temporary HOME, USERPROFILE, XDG_*, APPDATA and LOCALAPPDATA,
// git's global and system configuration neutralized, and a PATH on which the
// fake claude comes first. The real claude and the real Claude Code
// configuration are never touched.
//
// The package has no non-test code. Run it with
//
//	go test -race ./internal/e2e/...
//
// An opt-in smoke test against a real claude lives behind the realclaude build
// tag. See realclaude_test.go.
package e2e
