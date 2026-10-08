// Package pluginsource provides a profile.Source that reads profiles from a
// Claude Code plugin that is already installed: the "data-only plugin" way of
// shipping an org's profiles.
//
// Status: implemented (D-22), newer than the dir and git sources. This source
// is untested against a real Claude Code under managed policy. It only consumes what
// `claude plugin list --json` reports (through the injected Installed
// function) and never installs, enables or updates anything. D-13 keeps it
// behind the git source.
//
// # Behavior
//
// Prepare finds the plugin by its id. A plugin that is not installed, or
// reports no install path, is an error that includes the command to run:
//
//	/plugin install name@marketplace
//
// The profiles folder is Options.Path inside the install directory. Its parent
// is the source root, so mcp/registry.toml and prompts/ sit next to it as in
// any other source. The path must be relative and free of "..". Both the
// install directory and the root must resolve (after symlinks) to a place
// inside the install directory. Otherwise Prepare fails (SR4). A missing
// profiles folder means no profiles, not an error. The folder must be named
// "profiles", because this name decides whether prompts/ and mcp/ sit next to
// it, and a wrapper cannot pass the decision on.
//
// The plugin version enters the trust closure through Commit. The closure
// also hashes the profile files, registry entries and prompt bytes, so it
// still notices a plugin updated in place under the same version.
// ProtectedPluginIDs keeps the plugin that carries the org's profiles from
// being masked (SR3).
//
// # Scope, state and origin
//
// Prepare requires the plugin to be enabled and installed at user or managed
// scope. A project or local plugin comes from the repository the session
// starts in, which must never supply the shared profiles (SR3).
//
// The "@name" of a plugin id is only a local alias of a marketplace. So the
// launcher supplies Options.MarketplaceSource (from the read-only `claude
// plugin marketplace list --json`, where any unknown shape is an error), and
// ExpectedMarketplace when the configuration sets marketplace on the source.
// Prepare then binds the real source into ID and Locator, which keys the trust
// record to it. A mismatch with the expected source fails Prepare. The
// comparison follows these rules:
//
//   - owner/repo is compared only with a github marketplace, and a git URL
//     only with a git one.
//   - The https, ssh and scp forms of the same repository on any host compare
//     equal.
//   - A marketplace at a ref or sub-path never matches.
package pluginsource
