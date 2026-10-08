//go:build !e2eloopback

package config

// LoopbackHTTPAllowed reports whether ccshelf accepts a plain-http loopback
// update base URL. It is a compile-time constant. It is false in every build
// except one made with -tags e2eloopback. Only the end-to-end tests use that
// tag, to serve a fake release from 127.0.0.1. Release builds never set the
// tag, so the allowance does not exist in them.
const LoopbackHTTPAllowed = false
