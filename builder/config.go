package builder

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/packer-plugin-sdk/common"
	"github.com/hashicorp/packer-plugin-sdk/communicator"
	"github.com/hashicorp/packer-plugin-sdk/template/config"
	"github.com/hashicorp/packer-plugin-sdk/template/interpolate"
)

//go:generate packer-sdc mapstructure-to-hcl2 -type Config

// Config is the xcloud Packer builder configuration. It is decoded from
// the HCL/JSON template via mapstructure and validated in prepare().
type Config struct {
	common.PackerConfig `mapstructure:",squash"`

	// xcloud API connection. Falls back to CLOUD_CONSOLE_* environment
	// variables. APIEndpoint is the API host (with or without scheme);
	// APIToken is a tenant API key with the write:resources scope.
	APIEndpoint string `mapstructure:"api_endpoint"`
	APIToken    string `mapstructure:"api_token"`

	// Instance identity.
	RegionID string `mapstructure:"region_id"`
	Name     string `mapstructure:"name"`

	// Instance spec.
	CPUCores int    `mapstructure:"cpu_cores"`
	Memory   int    `mapstructure:"memory"`
	Disk     int    `mapstructure:"disk"`
	Network  string `mapstructure:"network"`

	// Base image source — exactly one of Image or PullImage. When PullImage
	// is set the builder registers a temporary image before create and
	// deletes it during cleanup.
	Image          string `mapstructure:"image"`
	PullImage      string `mapstructure:"pull_image"`
	PullUsername   string `mapstructure:"pull_username"`
	PullPassword   string `mapstructure:"pull_password"`
	PullCredential string `mapstructure:"pull_credential_id"`
	PullPrecache   bool   `mapstructure:"pull_precache"`

	// Access.
	AdminUsername string   `mapstructure:"admin_username"`
	SSHKeyIDs     []string `mapstructure:"ssh_key_ids"`

	// Reachability. When UseElasticIP is unset it defaults to true: the
	// builder allocates an elastic IP on create and SSHes to it. Set to
	// false to use the instance's private networkAddress instead.
	UseElasticIP *bool `mapstructure:"use_elastic_ip"`

	// Optional push target. When set, the builder shuts the VM down after
	// provisioning and pushes it as an OCI image to PushImage.
	PushImage      string `mapstructure:"push_image"`
	PushUsername   string `mapstructure:"push_username"`
	PushPassword   string `mapstructure:"push_password"`
	PushCredential string `mapstructure:"push_credential_id"`
	PushPrecache   bool   `mapstructure:"push_precache"`

	// If true, the instance (and any temp image/network/ssh-key) is kept
	// after the build completes instead of being torn down.
	KeepVM bool `mapstructure:"keep_vm"`

	// Polling tuning. Parsed into pollInterval / stateTimeout in prepare().
	PollInterval string `mapstructure:"poll_interval"`
	StateTimeout string `mapstructure:"state_timeout"`

	// Embedded communicator config (squash). Only "ssh" and "none" are
	// supported.
	Comm communicator.Config `mapstructure:",squash"`

	ctx interpolate.Context

	// Resolved, unexported values.
	useElasticIP  bool
	pollInterval  time.Duration
	stateTimeout  time.Duration
	createNetwork bool
}

func (c *Config) prepare() ([]string, error) {
	// Connection defaults / ENV fallbacks.
	if c.APIEndpoint == "" {
		c.APIEndpoint = os.Getenv("CLOUD_CONSOLE_API_ENDPOINT")
	}
	if c.APIToken == "" {
		c.APIToken = os.Getenv("CLOUD_CONSOLE_API_TOKEN")
	}

	// Identity defaults.
	if c.Name == "" {
		c.Name = "packer-" + uuid.New().String()[:8]
	}

	// Spec defaults.
	if c.CPUCores == 0 {
		c.CPUCores = 4
	}
	if c.Memory == 0 {
		c.Memory = 8
	}
	if c.Disk == 0 {
		c.Disk = 64
	}
	if c.Network == "" {
		c.Network = "default"
	}

	// Reachability default: true when unset.
	if c.UseElasticIP == nil {
		c.useElasticIP = true
	} else {
		c.useElasticIP = *c.UseElasticIP
	}

	// Communicator defaults.
	if c.Comm.Type == "" {
		c.Comm.Type = "ssh"
	}
	if c.Comm.SSHPort == 0 {
		c.Comm.SSHPort = 22
	}
	// Default the SSH login user so the communicator validates at
	// prepare/validate time. The real admin username is resolved at run
	// time from the server/image label in StepCreateInstance; this only
	// provides a sane default (admin_username, then "admin") so an explicit
	// ssh_username is not required.
	if c.Comm.SSHUsername == "" {
		if c.AdminUsername != "" {
			c.Comm.SSHUsername = c.AdminUsername
		} else {
			c.Comm.SSHUsername = "admin"
		}
	}

	// Polling defaults.
	if c.PollInterval == "" {
		c.PollInterval = "5s"
	}
	if c.StateTimeout == "" {
		c.StateTimeout = "20m"
	}

	var errs []error

	if d, err := time.ParseDuration(c.PollInterval); err != nil {
		errs = append(errs, fmt.Errorf("invalid 'poll_interval' duration %q: %w", c.PollInterval, err))
	} else {
		c.pollInterval = d
	}
	if d, err := time.ParseDuration(c.StateTimeout); err != nil {
		errs = append(errs, fmt.Errorf("invalid 'state_timeout' duration %q: %w", c.StateTimeout, err))
	} else {
		c.stateTimeout = d
	}

	// Required connection fields.
	if c.APIToken == "" {
		errs = append(errs, errors.New("'api_token' is required (or set CLOUD_CONSOLE_API_TOKEN)"))
	}
	if c.APIEndpoint == "" {
		errs = append(errs, errors.New("'api_endpoint' is required (or set CLOUD_CONSOLE_API_ENDPOINT)"))
	}
	if c.RegionID == "" {
		errs = append(errs, errors.New("'region_id' is required"))
	} else if _, err := uuid.Parse(c.RegionID); err != nil {
		errs = append(errs, fmt.Errorf("'region_id' must be a UUID: %w", err))
	}

	// Exactly one base image source.
	switch {
	case c.Image != "" && c.PullImage != "":
		errs = append(errs, errors.New("only one of 'image' or 'pull_image' can be used"))
	case c.Image == "" && c.PullImage == "":
		errs = append(errs, errors.New("one of 'image' or 'pull_image' is required"))
	}

	// Communicator restriction.
	if c.Comm.Type != "ssh" && c.Comm.Type != "none" {
		errs = append(errs, fmt.Errorf("only 'ssh' and 'none' communicators are supported, got %q", c.Comm.Type))
	}

	commErrs := c.Comm.Prepare(&c.ctx)
	errs = append(errs, commErrs...)

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return nil, nil
}

func (c *Config) decode(raws ...any) error {
	return config.Decode(c, &config.DecodeOpts{
		PluginType:         "xcloud",
		Interpolate:        true,
		InterpolateContext: &c.ctx,
		InterpolateFilter:  &interpolate.RenderFilter{},
	}, raws...)
}
