package builder

import (
	"testing"
	"time"

	"github.com/hashicorp/packer-plugin-sdk/communicator"
)

const testRegionID = "11111111-1111-1111-1111-111111111111"

func baseConfig() *Config {
	return &Config{
		APIEndpoint: "https://api.example.test",
		APIToken:    "tok",
		RegionID:    testRegionID,
		Image:       "macos-sequoia",
		Comm:        communicator.Config{Type: "none"},
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

func TestPrepareAgentCommunicatorMode(t *testing.T) {
	cfg := &Config{
		APIEndpoint:          "https://api.example.test",
		APIToken:             "tok",
		RegionID:             testRegionID,
		Image:                "macos-tahoe-agent",
		UseAgentCommunicator: true,
		// Deliberately leave Comm unset: agent mode must force "none"
		// and skip SSH validation without an explicit communicator block.
	}
	if _, err := cfg.prepare(); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if cfg.Comm.Type != "none" {
		t.Errorf("Comm.Type = %q, want \"none\" in agent mode", cfg.Comm.Type)
	}
	if cfg.useElasticIP {
		t.Error("useElasticIP = true, want false (agent mode default)")
	}
	if !needsKeyRegistration(cfg) {
		// agent mode forces comm type none → no key registration.
	} else {
		t.Error("needsKeyRegistration = true, want false in agent mode")
	}
}

func TestPrepareAgentModeRespectsExplicitElasticIP(t *testing.T) {
	cfg := &Config{
		APIEndpoint:          "https://api.example.test",
		APIToken:             "tok",
		RegionID:             testRegionID,
		Image:                "macos-tahoe-agent",
		UseAgentCommunicator: true,
	}
	tru := true
	cfg.UseElasticIP = &tru
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
		t.Fatal("expected error for missing region_id, got nil")
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
