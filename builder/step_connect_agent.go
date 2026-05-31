package builder

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/hashicorp/packer-plugin-sdk/multistep"
	"github.com/hashicorp/packer-plugin-sdk/packer"

	"github.com/studio-ch/packer-plugin-xcloud/apiclient"
)

// StepConnectAgent replaces communicator.StepConnect when the agent
// communicator is selected (use_agent_communicator = true). It waits for the
// in-guest xcloud-agent to become ready (by running a cheap no-op exec and
// retrying while the API reports 409 agent_not_ready), then publishes an
// agent-backed packer.Communicator into the state bag so
// commonsteps.StepProvision runs provisioners through it.
type StepConnectAgent struct{}

func (s *StepConnectAgent) Run(ctx context.Context, state multistep.StateBag) multistep.StepAction {
	cfg := state.Get("config").(*Config)
	client := state.Get("client").(*apiclient.Client)
	ui := state.Get("ui").(packer.Ui)
	id := state.Get("instance_id").(string)

	ui.Say("Waiting for the xcloud-agent to become ready ...")

	deadline := time.After(cfg.stateTimeout)
	ticker := time.NewTicker(cfg.pollInterval)
	defer ticker.Stop()

	probe := func() error {
		// Cheap readiness probe: a no-op exec. Output is discarded; we
		// only care that the stream opens (HTTP 200) and the agent runs
		// the command.
		_, err := client.ExecStream(
			ctx, id, []string{"/usr/bin/true"}, nil, "",
			io.Discard, io.Discard,
		)
		return err
	}

	// First attempt immediately so a healthy instance doesn't wait a full
	// poll interval.
	if err := probe(); err == nil {
		return s.connect(ctx, state, ui, client, id)
	} else if fatal := classifyProbeError(err); fatal != nil {
		state.Put("error", fatal)
		ui.Errorf("xcloud-agent is not usable: %v", fatal)
		return multistep.ActionHalt
	}

	var lastErr error
	for {
		select {
		case <-ctx.Done():
			state.Put("error", errors.New("context cancelled while waiting for the xcloud-agent"))
			ui.Error("Build was cancelled while waiting for the xcloud-agent")
			return multistep.ActionHalt
		case <-deadline:
			msg := "timed out waiting for the xcloud-agent to become ready"
			if lastErr != nil {
				msg += ": " + lastErr.Error()
			}
			state.Put("error", errors.New(msg))
			ui.Error(msg)
			return multistep.ActionHalt
		case <-ticker.C:
			err := probe()
			if err == nil {
				return s.connect(ctx, state, ui, client, id)
			}
			if fatal := classifyProbeError(err); fatal != nil {
				state.Put("error", fatal)
				ui.Errorf("xcloud-agent is not usable: %v", fatal)
				return multistep.ActionHalt
			}
			lastErr = err
			ui.Sayf("xcloud-agent not ready yet: %v", err)
		}
	}
}

func (s *StepConnectAgent) connect(
	ctx context.Context,
	state multistep.StateBag,
	ui packer.Ui,
	client *apiclient.Client,
	id string,
) multistep.StepAction {
	comm := newAgentCommunicator(ctx, client, id)
	state.Put("communicator", comm)
	ui.Say("xcloud-agent is ready; provisioners will run in-guest as root (no SSH)")
	return multistep.ActionContinue
}

// classifyProbeError returns a non-nil fatal error when the probe failure is
// not worth retrying (entitlement disabled, instance gone, auth). A retryable
// failure (409 agent_not_ready, transient transport error) returns nil so the
// caller keeps polling until state_timeout.
func classifyProbeError(err error) error {
	var apiErr *apiclient.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		case 403:
			return errors.New("the xcloud-agent exec API is not enabled for this tenant " +
				"(ask an operator to enable the agent-exec entitlement): " + apiErr.Error())
		case 401:
			return errors.New("authentication failed talking to the xcloud agent API: " + apiErr.Error())
		case 404:
			return errors.New("instance not found while connecting the xcloud-agent: " + apiErr.Error())
		case 409:
			// agent_not_ready / not running yet — retry.
			return nil
		}
		// Other 4xx (422 etc.) are configuration errors: don't spin.
		if apiErr.Status >= 400 && apiErr.Status < 500 {
			return apiErr
		}
	}
	// Transport errors and 5xx are transient — keep polling.
	return nil
}

func (s *StepConnectAgent) Cleanup(multistep.StateBag) {}
