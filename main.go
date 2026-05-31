package main

import (
	"fmt"
	"os"

	"github.com/hashicorp/packer-plugin-sdk/plugin"
	"github.com/hashicorp/packer-plugin-sdk/version"

	"github.com/studio-ch/packer-plugin-xcloud/builder"
)

// Version / VersionPrerelease are injected at build time via -ldflags by
// goreleaser (see .goreleaser.yml) so the binary self-reports the release
// tag. Packer matches this self-reported version against the release tag when
// installing, so it MUST track the tag. The defaults below apply to plain
// `go build` / dev builds.
var (
	Version           = "0.0.0"
	VersionPrerelease = "dev"
)

var pluginVersion = version.NewPluginVersion(Version, VersionPrerelease, "")

func main() {
	pps := plugin.NewSet()
	pps.SetVersion(pluginVersion)
	pps.RegisterBuilder(plugin.DEFAULT_NAME, new(builder.Builder))
	if err := pps.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}
