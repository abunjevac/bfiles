// Package version exposes the build version of bfiles.
package version

// Version is injected at build time via -ldflags. Defaults to "dev".
var Version = "dev" //nolint:gochecknoglobals
