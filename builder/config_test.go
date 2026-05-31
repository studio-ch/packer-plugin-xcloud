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
