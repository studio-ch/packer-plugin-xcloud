package builder

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/packer-plugin-sdk/multistep"
	"github.com/hashicorp/packer-plugin-sdk/packer"

	"github.com/studio-ch/packer-plugin-xcloud/apiclient"
)

// StepSSHKey registers a tenant SSH key for the build and stores its id in
// state so StepCreateInstance attaches it; cleanup deletes the key unless
// keep_vm is set. It has two modes:
//
//   - bring-your-own: when cfg.SSHAuthorizedKey is set, that public key is
//     registered as-is and no private key is generated or wired (the user
//     supplies the matching key via the native ssh_private_key_file).
//   - ephemeral: otherwise an ed25519 keypair is generated, the public key is
//     registered, and the private key is wired into the communicator.
type StepSSHKey struct{}

func (s *StepSSHKey) Run(ctx context.Context, state multistep.StateBag) multistep.StepAction {
	cfg := state.Get("config").(*Config)
	client := state.Get("client").(*apiclient.Client)
	ui := state.Get("ui").(packer.Ui)

	keyName := fmt.Sprintf("packer-%s-%d", cfg.Name, time.Now().Unix())
	if len(keyName) > 64 {
		keyName = keyName[:64]
	}

	var publicKey string
	if cfg.SSHAuthorizedKey != "" {
		// Bring-your-own: register the supplied public key, no private-key
		// wiring (the user provides ssh_private_key_file).
		ui.Say("Registering provided SSH public key ...")
		publicKey = cfg.SSHAuthorizedKey
	} else {
		ui.Say("Generating temporary SSH keypair ...")
		authorizedKey, privatePEM, err := generateTempSSHKey(keyName)
		if err != nil {
			state.Put("error", fmt.Errorf("failed to generate ssh key: %w", err))
			ui.Errorf("Could not generate temporary SSH key: %v", err)
			return multistep.ActionHalt
		}
		publicKey = authorizedKey
		cfg.Comm.SSHPrivateKey = privatePEM
	}

	key, err := client.CreateSSHKey(ctx, apiclient.CreateSSHKeyRequest{
		Name:      keyName,
		PublicKey: publicKey,
	})
	if err != nil {
		state.Put("error", fmt.Errorf("failed to register ssh key: %w", err))
		ui.Errorf("Could not register SSH key: %v", err)
		return multistep.ActionHalt
	}

	state.Put("temp_ssh_key_id", key.ID)
	ui.Sayf("SSH key %q registered", key.Name)
	return multistep.ActionContinue
}

func (s *StepSSHKey) Cleanup(state multistep.StateBag) {
	id, ok := state.Get("temp_ssh_key_id").(string)
	if !ok || id == "" {
		return
	}
	cfg := state.Get("config").(*Config)
	client := state.Get("client").(*apiclient.Client)
	ui := state.Get("ui").(packer.Ui)

	if cfg.KeepVM {
		ui.Say("Keeping temporary SSH key because keep_vm=true")
		return
	}

	ui.Say("Deleting temporary SSH key ...")
	ctx, cancel := cleanupContext()
	defer cancel()
	if err := client.DeleteSSHKey(ctx, id); err != nil {
		ui.Errorf("Could not delete temporary SSH key: %v", err)
		return
	}
	ui.Say("Temporary SSH key deleted")
}
