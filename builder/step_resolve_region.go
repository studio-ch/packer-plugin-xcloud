package builder

import (
	"context"
	"fmt"

	"github.com/hashicorp/packer-plugin-sdk/multistep"
	"github.com/hashicorp/packer-plugin-sdk/packer"

	"github.com/studio-ch/packer-plugin-xcloud/apiclient"
)

// StepResolveRegion resolves a region slug (cfg.Region, e.g. "ZRH1") to its
// region UUID when region_id was not supplied directly, writing the resolved
// UUID back into cfg.RegionID. It runs first so every later region-scoped step
// (register image, create network, create instance) and the artifact see the
// resolved UUID. A no-op when region_id is already set.
type StepResolveRegion struct{}

func (s *StepResolveRegion) Run(ctx context.Context, state multistep.StateBag) multistep.StepAction {
	cfg := state.Get("config").(*Config)

	// region_id supplied directly (or already resolved): nothing to do.
	if cfg.RegionID != "" || cfg.Region == "" {
		return multistep.ActionContinue
	}

	client := state.Get("client").(*apiclient.Client)
	ui := state.Get("ui").(packer.Ui)

	ui.Sayf("Resolving region %q ...", cfg.Region)
	id, err := client.ResolveRegionID(ctx, cfg.Region)
	if err != nil {
		state.Put("error", fmt.Errorf("failed to resolve region %q: %w", cfg.Region, err))
		ui.Errorf("Could not resolve region %q: %v", cfg.Region, err)
		return multistep.ActionHalt
	}

	cfg.RegionID = id
	ui.Sayf("Region %q resolved to %s", cfg.Region, id)
	return multistep.ActionContinue
}

func (s *StepResolveRegion) Cleanup(multistep.StateBag) {}
