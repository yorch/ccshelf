//go:build !e2eloopback

package config

// LoopbackHTTPAllowed reports whether a plain-http loopback update base URL
// is accepted. It is a compile-time constant that is false in every build
// except one made with -tags e2eloopback, which only the end-to-end tests use
// to serve a fake release from 127.0.0.1. Release builds never set the tag,
// so the allowance does not exist in them.
const LoopbackHTTPAllowed = false
