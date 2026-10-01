package version

// Version is overridden at build time with -ldflags "-X .../version.Version=...".
var Version = "0.1.0-dev"

// Protocol is bumped on incompatible RPC or state changes.
const Protocol = 1

// StateSchema is the version of state.json.
const StateSchema = 1
