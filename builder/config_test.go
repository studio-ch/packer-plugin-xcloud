package builder

import (
	"testing"
	"time"

	"github.com/hashicorp/packer-plugin-sdk/communicator"
)

const testRegionID = "11111111-1111-1111-1111-111111111111"

func boolPtr(b bool) *bool { return &b }

// baseConfig returns a minimal SSH-path config. use_agent_communicator is
// explicitly disabled so the SSH-oriented assertions hold regardless of the
// (now true) default; agent-mode tests construct their own config.
func baseConfig() *Config {
	return &Config{
		APIEndpoint:          "https://api.example.test",
		APIToken:             "tok",
		RegionID:             testRegionID,
		Image:                "macos-sequoia",
		Comm:                 communicator.Config{Type: "none"},
		UseAgentCommunicator: boolPtr(false),
	}
}

func TestPrepareAppliesDefaults(t *testing.T) {
	t.Setenv("CLOUD_CONSOLE_API_ENDPOINT", "")
	t.Setenv("CLOUD_CONSOLE_API_TOKEN", "")

	cfg := baseConfig()
	if _, err := cfg.prepare(); err != nil {
		t.Fatalf("prepare: %v", err)
	}

	if cfg.CPUCores != 4 {
		t.Errorf("CPUCores = %d, want 4", cfg.CPUCores)
	}
	if cfg.Memory != 8 {
		t.Errorf("Memory = %d, want 8", cfg.Memory)
	}
	if cfg.Disk != 64 {
		t.Errorf("Disk = %d, want 64", cfg.Disk)
	}
	if cfg.Network != "default" {
		t.Errorf("Network = %q, want \"default\"", cfg.Network)
	}
	if !cfg.useElasticIP {
		t.Error("useElasticIP = false, want true (default)")
	}
	if cfg.pollInterval != 5*time.Second {
		t.Errorf("pollInterval = %v, want 5s", cfg.pollInterval)
	}
	if cfg.stateTimeout != 20*time.Minute {
		t.Errorf("stateTimeout = %v, want 20m", cfg.stateTimeout)
	}
	if cfg.Name == "" {
		t.Error("Name was not defaulted")
	}
}

func TestPrepareUseElasticIPExplicitFalse(t *testing.T) {
	cfg := baseConfig()
	f := false
	cfg.UseElasticIP = &f
	if _, err := cfg.prepare(); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if cfg.useElasticIP {
		t.Error("useElasticIP = true, want false (explicitly set)")
	}
}

func TestPrepareAgentCommunicatorDefaultsOn(t *testing.T) {
	// use_agent_communicator unset (nil pointer) must default to true: the
	// agent path is forced even though no communicator block is supplied.
	cfg := &Config{
		APIEndpoint: "https://api.example.test",
		APIToken:    "tok",
		RegionID:    testRegionID,
		Image:       "macos-tahoe-agent",
		// UseAgentCommunicator deliberately left nil → default true.
	}
	if _, err := cfg.prepare(); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if !cfg.useAgentCommunicator {
		t.Error("useAgentCommunicator = false, want true (nil default)")
	}
	if cfg.Comm.Type != "none" {
		t.Errorf("Comm.Type = %q, want \"none\" in default agent mode", cfg.Comm.Type)
	}
	if cfg.useElasticIP {
		t.Error("useElasticIP = true, want false (agent mode default)")
	}
	if needsKeyRegistration(cfg) {
		t.Error("needsKeyRegistration = true, want false in agent mode")
	}
}

func TestPrepareAgentCommunicatorExplicitTrue(t *testing.T) {
	cfg := &Config{
		APIEndpoint:          "https://api.example.test",
		APIToken:             "tok",
		RegionID:             testRegionID,
		Image:                "macos-tahoe-agent",
		UseAgentCommunicator: boolPtr(true),
		// Deliberately leave Comm unset: agent mode must force "none"
		// and skip SSH validation without an explicit communicator block.
	}
	if _, err := cfg.prepare(); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if !cfg.useAgentCommunicator {
		t.Error("useAgentCommunicator = false, want true (explicit true)")
	}
	if cfg.Comm.Type != "none" {
		t.Errorf("Comm.Type = %q, want \"none\" in agent mode", cfg.Comm.Type)
	}
	if cfg.useElasticIP {
		t.Error("useElasticIP = true, want false (agent mode default)")
	}
	if needsKeyRegistration(cfg) {
		t.Error("needsKeyRegistration = true, want false in agent mode")
	}
}

func TestPrepareAgentCommunicatorExplicitFalseUsesSSH(t *testing.T) {
	// use_agent_communicator = false → SSH path: communicator defaults to
	// "ssh", elastic IP defaults to true, and an ephemeral key is registered.
	cfg := &Config{
		APIEndpoint:          "https://api.example.test",
		APIToken:             "tok",
		RegionID:             testRegionID,
		Image:                "macos-sequoia",
		UseAgentCommunicator: boolPtr(false),
	}
	if _, err := cfg.prepare(); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if cfg.useAgentCommunicator {
		t.Error("useAgentCommunicator = true, want false (explicit false)")
	}
	if cfg.Comm.Type != "ssh" {
		t.Errorf("Comm.Type = %q, want \"ssh\" (SSH path)", cfg.Comm.Type)
	}
	if !cfg.useElasticIP {
		t.Error("useElasticIP = false, want true (SSH path default)")
	}
	if !needsKeyRegistration(cfg) {
		t.Error("needsKeyRegistration = false, want true (SSH path, ephemeral key)")
	}
}

func TestPrepareAgentModeRespectsExplicitElasticIP(t *testing.T) {
	cfg := &Config{
		APIEndpoint:          "https://api.example.test",
		APIToken:             "tok",
		RegionID:             testRegionID,
		Image:                "macos-tahoe-agent",
		UseAgentCommunicator: boolPtr(true),
	}
	cfg.UseElasticIP = boolPtr(true)
	if _, err := cfg.prepare(); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if !cfg.useElasticIP {
		t.Error("useElasticIP = false, want true (explicitly set even in agent mode)")
	}
}

func TestPrepareEnvFallback(t *testing.T) {
	t.Setenv("CLOUD_CONSOLE_API_ENDPOINT", "https://env.example.test")
	t.Setenv("CLOUD_CONSOLE_API_TOKEN", "env-token")

	cfg := &Config{
		RegionID: testRegionID,
		Image:    "macos-sequoia",
		Comm:     communicator.Config{Type: "none"},
	}
	if _, err := cfg.prepare(); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if cfg.APIEndpoint != "https://env.example.test" {
		t.Errorf("APIEndpoint = %q, want env fallback", cfg.APIEndpoint)
	}
	if cfg.APIToken != "env-token" {
		t.Errorf("APIToken = %q, want env fallback", cfg.APIToken)
	}
}

func TestPrepareMissingAPIToken(t *testing.T) {
	t.Setenv("CLOUD_CONSOLE_API_TOKEN", "")
	cfg := baseConfig()
	cfg.APIToken = ""
	if _, err := cfg.prepare(); err == nil {
		t.Fatal("expected error for missing api_token, got nil")
	}
}

func TestPrepareMissingRegionID(t *testing.T) {
	cfg := baseConfig()
	cfg.RegionID = ""
	if _, err := cfg.prepare(); err == nil {
		t.Fatal("expected error when neither region nor region_id set, got nil")
	}
}

func TestPrepareAcceptsRegionSlug(t *testing.T) {
	cfg := baseConfig()
	cfg.RegionID = ""
	cfg.Region = "BIT1"
	if _, err := cfg.prepare(); err != nil {
		t.Fatalf("prepare with region slug: %v", err)
	}
	// region is resolved at build time, not in prepare(): RegionID stays empty.
	if cfg.RegionID != "" {
		t.Errorf("RegionID = %q, want empty (resolved at build time, not in prepare)", cfg.RegionID)
	}
}

func TestPrepareRejectsBothRegionAndRegionID(t *testing.T) {
	cfg := baseConfig() // RegionID already set
	cfg.Region = "BIT1"
	if _, err := cfg.prepare(); err == nil {
		t.Fatal("expected error when both region and region_id set, got nil")
	}
}

func TestPrepareRejectsNeitherRegion(t *testing.T) {
	cfg := baseConfig()
	cfg.RegionID = ""
	cfg.Region = ""
	if _, err := cfg.prepare(); err == nil {
		t.Fatal("expected error when neither region nor region_id set, got nil")
	}
}

func TestPrepareRejectsNonUUIDRegionID(t *testing.T) {
	cfg := baseConfig()
	cfg.RegionID = "not-a-uuid"
	if _, err := cfg.prepare(); err == nil {
		t.Fatal("expected error for non-UUID region_id, got nil")
	}
}

func TestPrepareBothImageSources(t *testing.T) {
	cfg := baseConfig()
	cfg.PullImage = "ghcr.io/org/base:tag"
	if _, err := cfg.prepare(); err == nil {
		t.Fatal("expected error when both image and pull_image set, got nil")
	}
}

func TestPrepareNoImageSource(t *testing.T) {
	cfg := baseConfig()
	cfg.Image = ""
	if _, err := cfg.prepare(); err == nil {
		t.Fatal("expected error when neither image nor pull_image set, got nil")
	}
}

func TestPrepareDefaultsSSHUsernameFromAdminUsername(t *testing.T) {
	cfg := baseConfig()
	cfg.Comm = communicator.Config{Type: "ssh"}
	cfg.AdminUsername = "ubuntu"
	if _, err := cfg.prepare(); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if cfg.Comm.SSHUsername != "ubuntu" {
		t.Errorf("Comm.SSHUsername = %q, want %q (from admin_username)", cfg.Comm.SSHUsername, "ubuntu")
	}
}

func TestPrepareDefaultsSSHUsernameToAdmin(t *testing.T) {
	cfg := baseConfig()
	cfg.Comm = communicator.Config{Type: "ssh"}
	if _, err := cfg.prepare(); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if cfg.Comm.SSHUsername != "admin" {
		t.Errorf("Comm.SSHUsername = %q, want %q (default)", cfg.Comm.SSHUsername, "admin")
	}
}

func TestPreparePreservesExplicitSSHUsername(t *testing.T) {
	cfg := baseConfig()
	cfg.Comm = communicator.Config{Type: "ssh"}
	cfg.Comm.SSHUsername = "root"
	cfg.AdminUsername = "ubuntu"
	if _, err := cfg.prepare(); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if cfg.Comm.SSHUsername != "root" {
		t.Errorf("Comm.SSHUsername = %q, want %q (explicit ssh_username preserved)", cfg.Comm.SSHUsername, "root")
	}
}

func TestPrepareRejectsWinRM(t *testing.T) {
	cfg := baseConfig()
	cfg.Comm = communicator.Config{Type: "winrm"}
	if _, err := cfg.prepare(); err == nil {
		t.Fatal("expected error for unsupported communicator, got nil")
	}
}

func TestNeedsKeyRegistration(t *testing.T) {
	const samplePubKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIabc comment"

	tests := []struct {
		name string
		mut  func(c *Config)
		want bool
	}{
		{
			name: "ssh, nothing provided -> ephemeral",
			mut:  func(c *Config) { c.Comm.Type = "ssh" },
			want: true,
		},
		{
			name: "ssh, ssh_authorized_key set -> register",
			mut: func(c *Config) {
				c.Comm.Type = "ssh"
				c.SSHAuthorizedKey = samplePubKey
			},
			want: true,
		},
		{
			name: "ssh, ssh_key_ids set -> skip",
			mut: func(c *Config) {
				c.Comm.Type = "ssh"
				c.SSHKeyIDs = []string{"key-1"}
			},
			want: false,
		},
		{
			name: "ssh, native ssh_private_key_file set -> skip",
			mut: func(c *Config) {
				c.Comm.Type = "ssh"
				c.Comm.SSHPrivateKeyFile = "/tmp/id_ed25519"
			},
			want: false,
		},
		{
			name: "ssh, authorized_key wins over native private key file -> register",
			mut: func(c *Config) {
				c.Comm.Type = "ssh"
				c.SSHAuthorizedKey = samplePubKey
				c.Comm.SSHPrivateKeyFile = "/tmp/id_ed25519"
			},
			want: true,
		},
		{
			name: "ssh, ssh_key_ids set even with authorized_key -> skip",
			mut: func(c *Config) {
				c.Comm.Type = "ssh"
				c.SSHKeyIDs = []string{"key-1"}
				c.SSHAuthorizedKey = samplePubKey
			},
			want: false,
		},
		{
			name: "non-ssh communicator -> skip",
			mut:  func(c *Config) { c.Comm.Type = "none" },
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{}
			tt.mut(cfg)
			if got := needsKeyRegistration(cfg); got != tt.want {
				t.Errorf("needsKeyRegistration() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLooksLikeOpenSSHPublicKey(t *testing.T) {
	valid := []string{
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIabc comment",
		"ssh-rsa AAAAB3NzaC1yc2EAAAA...",
		"  ecdsa-sha2-nistp256 AAAAE2Vj...",
		"sk-ssh-ed25519@openssh.com AAAA...",
	}
	for _, s := range valid {
		if !looksLikeOpenSSHPublicKey(s) {
			t.Errorf("looksLikeOpenSSHPublicKey(%q) = false, want true", s)
		}
	}

	invalid := []string{
		"",
		"-----BEGIN OPENSSH PRIVATE KEY-----",
		"/home/me/.ssh/id_ed25519.pub",
		"not a key",
	}
	for _, s := range invalid {
		if looksLikeOpenSSHPublicKey(s) {
			t.Errorf("looksLikeOpenSSHPublicKey(%q) = true, want false", s)
		}
	}
}

func TestPrepareAcceptsValidSSHAuthorizedKey(t *testing.T) {
	cfg := baseConfig()
	cfg.Comm = communicator.Config{Type: "ssh"}
	cfg.SSHAuthorizedKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIabc packer@test"
	if _, err := cfg.prepare(); err != nil {
		t.Fatalf("prepare with valid ssh_authorized_key: %v", err)
	}
}

func TestPrepareRejectsMalformedSSHAuthorizedKey(t *testing.T) {
	cfg := baseConfig()
	cfg.Comm = communicator.Config{Type: "ssh"}
	cfg.SSHAuthorizedKey = "-----BEGIN OPENSSH PRIVATE KEY-----"
	if _, err := cfg.prepare(); err == nil {
		t.Fatal("expected error for malformed ssh_authorized_key, got nil")
	}
}

func TestDecodeSSHAuthorizedKey(t *testing.T) {
	const pub = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIabc packer@test"
	cfg := &Config{}
	raw := map[string]interface{}{
		"api_endpoint":       "https://api.example.test",
		"api_token":          "tok",
		"region_id":          testRegionID,
		"image":              "macos-sequoia",
		"communicator":       "ssh",
		"ssh_authorized_key": pub,
	}
	if err := cfg.decode(raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.SSHAuthorizedKey != pub {
		t.Errorf("SSHAuthorizedKey = %q, want %q", cfg.SSHAuthorizedKey, pub)
	}
}
