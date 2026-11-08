// Package version holds the build-stamped version string, injected at link
// time:
//
//	go build -ldflags "-X github.com/YunusAndreasson/safina/internal/version.Version=v1.2.3"
//
// It feeds the User-Agent on every API request so apiserver audit logs on
// production clusters attribute safina traffic to an exact build, and backs
// `safina --version`.
package version

var Version = "dev"
