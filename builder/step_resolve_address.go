package builder

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/packer-plugin-sdk/multistep"
	"github.com/hashicorp/packer-plugin-sdk/packer"

	"github.com/studio-ch/packer-plugin-xcloud/apiclient"
)

// StepResolveAddress determines the address Packer connects to. When
// use_elastic_ip is set it polls the instance's elastic IP until it has a
// usable public address (status "ready" or "bound"); otherwise it uses the
// instance's private networkAddress. The result is stored in state as "vm_ip".
// Skipped for the "none" communicator.
type StepResolveAddress struct{}

// elasticIPUsable reports the public address and whether the elastic IP is
// usable for an SSH connection. An IP is usable as soon as it has a non-empty
// publicAddress and its status is "ready" or "bound": on some upstreams the
// floating IP never flips to "bound" even though the address is already fully
// routable, so "ready" + an address is treated as usable too. Without an
// address the IP is never usable regardless of status.
func elasticIPUsable(eip apiclient.ElasticIP) (string, bool) {
	addr := derefStr(eip.PublicAddress)
	if addr == "" {
		return "", false
	}
	switch eip.Status {
	case "ready", "bound":
		return addr, true
	default:
		return "", false
	}
}

func (s *StepResolveAddress) Run(ctx context.Context, state multistep.StateBag) multistep.StepAction {
	cfg := state.Get("config").(*Config)
	if cfg.Comm.Type == "none" {
		return multistep.ActionContinue
	}

	client := state.Get("client").(*apiclient.Client)
	ui := state.Get("ui").(packer.Ui)
	id := state.Get("instance_id").(string)

	if !cfg.useElasticIP {
		ui.Say("Resolving instance network address ...")
		ip, action := s.pollNetworkAddress(ctx, state, cfg, client, ui, id)
		if action != multistep.ActionContinue {
			return action
		}
		state.Put("vm_ip", ip)
		ui.Sayf("Instance reachable at %s", ip)
		return multistep.ActionContinue
	}

	ui.Say("Waiting for elastic IP to become usable ...")
	deadline := time.After(cfg.stateTimeout)
	ticker := time.NewTicker(cfg.pollInterval)
	defer ticker.Stop()

	var lastStatus string
	for {
		select {
		case <-ctx.Done():
			state.Put("error", fmt.Errorf("context cancelled while waiting for elastic IP"))
			ui.Error("Build was cancelled while waiting for the elastic IP")
			return multistep.ActionHalt
		case <-deadline:
			state.Put("error", fmt.Errorf("timed out waiting for elastic IP to become usable"))
			ui.Error("Timed out waiting for the elastic IP to become usable")
			return multistep.ActionHalt
		case <-ticker.C:
			eips, err := client.ListElasticIPsByInstance(ctx, id)
			if err != nil {
				ui.Errorf("Could not check elastic IP status yet: %v", err)
				continue
			}
			for _, eip := range eips {
				if addr, ok := elasticIPUsable(eip); ok {
					// Capture the elastic IP id so Cleanup releases it
					// on teardown — for both "ready" and "bound", since
					// the instance-delete cascade is not relied upon.
					state.Put("elastic_ip_id", eip.ID)
					state.Put("vm_ip", addr)
					ui.Sayf("Instance reachable at %s (elastic IP %s)", addr, eip.Status)
					return multistep.ActionContinue
				}
			}
			status := "allocating"
			if len(eips) > 0 {
				status = eips[0].Status
			}
			if status != lastStatus {
				ui.Sayf("Elastic IP status: %s", status)
				lastStatus = status
			}
		}
	}
}

func (s *StepResolveAddress) pollNetworkAddress(
	ctx context.Context,
	state multistep.StateBag,
	cfg *Config,
	client *apiclient.Client,
	ui packer.Ui,
	id string,
) (string, multistep.StepAction) {
	deadline := time.After(cfg.stateTimeout)
	ticker := time.NewTicker(cfg.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			state.Put("error", fmt.Errorf("context cancelled while waiting for network address"))
			ui.Error("Build was cancelled while waiting for a network address")
			return "", multistep.ActionHalt
		case <-deadline:
			state.Put("error", fmt.Errorf("timed out waiting for instance network address"))
			ui.Error("Timed out waiting for instance network address")
			return "", multistep.ActionHalt
		case <-ticker.C:
			inst, err := client.GetInstance(ctx, id)
			if err != nil {
				ui.Errorf("Could not read instance address yet: %v", err)
				continue
			}
			if addr := derefStr(inst.NetworkAddress); addr != "" {
				return addr, multistep.ActionContinue
			}
		}
	}
}

func (s *StepResolveAddress) Cleanup(state multistep.StateBag) {
	id, ok := state.Get("elastic_ip_id").(string)
	if !ok || id == "" {
		return
	}
	cfg := state.Get("config").(*Config)
	client := state.Get("client").(*apiclient.Client)
	ui := state.Get("ui").(packer.Ui)

	if cfg.KeepVM {
		ui.Say("Keeping elastic IP because keep_vm=true")
		return
	}

	ui.Say("Releasing elastic IP ...")
	ctx, cancel := cleanupContext()
	defer cancel()
	if err := client.ReleaseElasticIP(ctx, id); err != nil {
		// Best-effort: an elastic IP still bound to a not-yet-deleted
		// instance may reject release; the instance deletion that
		// follows tears it down anyway.
		ui.Errorf("Could not release elastic IP (will be cleaned up with the instance): %v", err)
		return
	}
	ui.Say("Elastic IP released")
}
