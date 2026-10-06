package schema

import _ "embed"

//go:embed profile.schema.json
var profileJSON []byte

//go:embed config.schema.json
var configJSON []byte

//go:embed mcp-registry.schema.json
var mcpRegistryJSON []byte

func clone(b []byte) []byte { return append([]byte(nil), b...) }

// Profile returns the JSON Schema (draft 2020-12) for a profile manifest.
func Profile() []byte { return clone(profileJSON) }

// Config returns the JSON Schema for the user config file.
func Config() []byte { return clone(configJSON) }

// MCPRegistry returns the JSON Schema for mcp/registry.toml.
func MCPRegistry() []byte { return clone(mcpRegistryJSON) }
