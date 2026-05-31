package builder

import (
	"errors"
	"fmt"
	"os"
	"strings"
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

	// Instance identity. Exactly one of Region (a friendly region slug, e.g.
	// "BIT1", resolved to the UUID at build time) or RegionID (the region
	// UUID) must be set. Region is resolved by StepResolveRegion before any
	// region-scoped API call; the resolved UUID is written back into RegionID.
	Region   string `mapstructure:"region"`
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

	// Bring-your-own SSH public key. When set, the raw OpenSSH public key is
	// registered as a tenant key for the duration of the build and attached
	// to the instance (then deleted on cleanup unless keep_vm). No private
	// key is generated or wired — pair it with the native ssh_private_key_file
	// so the communicator can authenticate. Mutually independent from
	// ssh_key_ids (pre-registered keys) and the auto-generated ephemeral key.
	//
	// NOTE: the template attribute is "ssh_authorized_key", not
	// "ssh_public_key": the squashed communicator.Config already reserves the
	// "ssh_public_key" mapstructure tag for an internal field, so reusing it
	// collides at code-generation time.
	SSHAuthorizedKey string `mapstructure:"ssh_authorized_key"`

	// Reachability. When UseElasticIP is unset it defaults to true: the
	// builder allocates an elastic IP on create and SSHes to it. Set to
	// false to use the instance's private networkAddress instead.
	UseElasticIP *bool `mapstructure:"use_elastic_ip"`

	// UseAgentCommunicator runs provisioners through the in-guest
	// xcloud-agent (over the Cloud Console agent exec/file API) instead of
	// SSH. Because the agent runs as root there is no SSH-reachability
	// requirement, no public IP, no SSH key and no sudo password. It
	// requires the tenant to have the agent-exec entitlement enabled on the
	// Cloud Console. When enabled the builder forces comm type "none",
	// defaults use_elastic_ip to false, and skips StepSSHKey +
	// communicator.StepConnect in favour of StepConnectAgent.
	//
	// Defaults to true when unset: set use_agent_communicator = false to
	// opt back into the SSH path. A nil pointer (unset) is treated as true;
	// the resolved value lands in useAgentCommunicator during prepare().
	UseAgentCommunicator *bool `mapstructure:"use_agent_communicator"`

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
	useAgentCommunicator bool
	useElasticIP         bool
	pollInterval         time.Duration
	stateTimeout         time.Duration
	createNetwork        bool
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

	// Agent communicator default: true when unset (nil pointer). Provisioners
	// then run in-guest via the xcloud-agent rather than SSH. Set
	// use_agent_communicator = false to opt back into the SSH path. Mirror of
	// the UseElasticIP *bool resolution below.
	if c.UseAgentCommunicator == nil {
		c.useAgentCommunicator = true
	} else {
		c.useAgentCommunicator = *c.UseAgentCommunicator
	}

	// Agent communicator mode: provisioners run in-guest via the
	// xcloud-agent, not SSH. Force comm type "none" so the SSH
	// communicator validation is skipped (no key / username required) and
	// no StepConnect SSH dial is attempted.
	if c.useAgentCommunicator {
		c.Comm.Type = "none"
	}

	// Reachability default: true when unset, except in agent mode where no
	// public IP is needed (the agent is reached over the Cloud Console
	// gateway, not the VM's network).
	if c.UseElasticIP == nil {
		c.useElasticIP = !c.useAgentCommunicator
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
	// Exactly one of region (a slug resolved to a UUID at build time) or
	// region_id (the UUID itself). region_id, when set, must be a UUID.
	switch {
	case c.Region != "" && c.RegionID != "":
		errs = append(errs, errors.New("only one of 'region' or 'region_id' can be used"))
	case c.Region == "" && c.RegionID == "":
		errs = append(errs, errors.New("one of 'region' (slug, e.g. \"BIT1\") or 'region_id' (UUID) is required"))
	case c.RegionID != "":
		if _, err := uuid.Parse(c.RegionID); err != nil {
			errs = append(errs, fmt.Errorf("'region_id' must be a UUID: %w", err))
		}
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

	// Lenient sanity check for a bring-your-own public key. The API validates
	// authoritatively; this only catches obvious mistakes (e.g. a private key
	// or a file path pasted in by accident).
	if c.SSHAuthorizedKey != "" && !looksLikeOpenSSHPublicKey(c.SSHAuthorizedKey) {
		errs = append(errs, errors.New("'ssh_authorized_key' does not look like an OpenSSH public key (expected a line starting with e.g. 'ssh-ed25519', 'ssh-rsa', 'ecdsa-...' or 'sk-...')"))
	}

	commErrs := c.Comm.Prepare(&c.ctx)
	errs = append(errs, commErrs...)

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return nil, nil
}

// needsKeyRegistration reports whether the build should register an SSH key
// (StepSSHKey) before creating the instance. It is a small pure function so it
// can be unit-tested. The decision: only when the ssh communicator is used and
// no pre-registered ssh_key_ids were supplied; then register when either a
// bring-your-own ssh_authorized_key is set, or nothing else (no native
// ssh_private_key_file) is provided — in which case an ephemeral key is
// generated. When a native ssh_private_key_file is supplied (and no
// ssh_authorized_key), key registration is skipped: the user manages the key
// material themselves.
func needsKeyRegistration(c *Config) bool {
	if c.Comm.Type != "ssh" {
		return false
	}
	if len(c.SSHKeyIDs) > 0 {
		return false
	}
	if c.SSHAuthorizedKey != "" {
		return true
	}
	return c.Comm.SSHPrivateKeyFile == ""
}

// looksLikeOpenSSHPublicKey is a permissive check that s resembles a single
// OpenSSH public key line (e.g. "ssh-ed25519 AAAA... comment"). It only guards
// against obvious mistakes; the API validates authoritatively.
func looksLikeOpenSSHPublicKey(s string) bool {
	s = strings.TrimSpace(s)
	for _, prefix := range []string{"ssh-", "ecdsa-", "sk-"} {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

func (c *Config) decode(raws ...any) error {
	return config.Decode(c, &config.DecodeOpts{
		PluginType:         "xcloud",
		Interpolate:        true,
		InterpolateContext: &c.ctx,
		InterpolateFilter:  &interpolate.RenderFilter{},
	}, raws...)
}
