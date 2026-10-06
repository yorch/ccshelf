// Package pluginsource provides a profile.Source that reads profiles from a
// Claude Code plugin that is already installed: the "data-only plugin" way of
// shipping an org's profiles.
//
// Status: planned for later, after testing under managed policy. This source
// is untested against a real Claude Code; it only consumes what
// `claude plugin list --json` reports (through the injected Installed
// function) and never installs, enables or updates anything. D-13 keeps it
// behind the git source.
//
// # Behavior
//
// Prepare calls Options.Installed, finds the plugin by its id (name@marketplace)
// and takes its InstallPath. A plugin that is not installed, or reports no
// install path, is an error that includes the command to run:
//
//	/plugin install name@marketplace
//
// The profiles folder is Options.Path (default "profiles") inside the install
// directory; its parent is the source root, so mcp/registry.toml and
// prompts/ sit next to it as in any other source. The path must be relative
// and free of "..", and both the install directory and the root must resolve
// (after symlinks) to a place inside the install directory, otherwise Prepare
// fails (SR4). A missing profiles folder means no profiles, not an error.
//
// Commit returns "plugin:<version>" ("plugin:unversioned" when Claude Code
// reports none), which enters the trust closure through the source item. The
// closure also hashes the profile files, registry entries and prompt bytes
// themselves, so a plugin updated in place under the same version is still
// noticed. ID is "plugin:<name@marketplace>"; it contains no machine path.
//
// ProtectedPluginIDs returns the plugin itself so the settings spec can never
// mask the plugin that carries the org's profiles (SR3).
//
// # Scope, state and origin
//
// Prepare requires the plugin to be enabled and installed at user or managed
// scope: a project or local plugin comes from the repository the session
// starts in, which must never supply the shared profiles (SR3). The "@name"
// of a plugin id is only a local alias of a marketplace, so Options may
// supply MarketplaceSource (the real source the marketplace was added from)
// and ExpectedMarketplace (what the organization requires). When the lookup
// is given, the real source is bound into ID and Locator ("plugin:<id> from
// <source>"), which keys the trust record to it; a mismatch with the expected
// source fails Prepare. The profiles folder must be named "profiles", because
// whether prompts/ and mcp/ sit next to it is decided from that name and a
// wrapper cannot pass the decision on. Root is the directory source's root.
package pluginsource
