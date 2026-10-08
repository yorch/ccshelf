//go:build e2eloopback

package config

// LoopbackHTTPAllowed is true only in binaries built with -tags e2eloopback
// (the end-to-end tests). See loopback_prod.go.
const LoopbackHTTPAllowed = true
