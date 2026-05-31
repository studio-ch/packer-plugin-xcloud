package builder

import (
	"context"
	"errors"

	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/hashicorp/packer-plugin-sdk/communicator"
	"github.com/hashicorp/packer-plugin-sdk/multistep"
	"github.com/hashicorp/packer-plugin-sdk/multistep/commonsteps"
	"github.com/hashicorp/packer-plugin-sdk/packer"

	"github.com/studio-ch/packer-plugin-xcloud/apiclient"
)

// BuilderId uniquely identifies artifacts produced by this builder.
const BuilderId = "studio-ch.xcloud"

type Builder struct {
	config Config
	runner multistep.Runner
}

func (b *Builder) ConfigSpec() hcldec.ObjectSpec {
	return b.config.FlatMapstructure().HCL2Spec()
}

func (b *Builder) Prepare(raws ...any) ([]string, []string, error) {
	if err := b.config.decode(raws...); err != nil {
		return nil, nil, err
	}
	warnings, err := b.config.prepare()
	if err != nil {
		return nil, warnings, err
	}
	return nil, warnings, nil
}

func (b *Builder) Run(ctx context.Context, ui packer.Ui, hook packer.Hook) (packer.Artifact, error) {
	client := apiclient.New(b.config.APIEndpoint, b.config.APIToken, nil)

	ui.Sayf("Using xcloud API at %s", b.config.APIEndpoint)

	state := new(multistep.BasicStateBag)
	state.Put("config", &b.config)
	state.Put("client", client)
	state.Put("hook", hook)
	state.Put("ui", ui)

	useSSH := b.config.Comm.Type == "ssh"

	var steps []multistep.Step
	steps = append(steps, &StepRegisterImage{})

	// Register an SSH key before StepCreateInstance (so its id is included in
	// the create body) when the build needs one: either a bring-your-own
	// ssh_authorized_key to register, or an ephemeral key to generate. Skipped
	// when ssh_key_ids are supplied or the user provides a native
	// ssh_private_key_file. See needsKeyRegistration.
	if needsKeyRegistration(&b.config) {
		steps = append(steps, &StepSSHKey{})
	}

	steps = append(steps,
		&StepCreateNetwork{},
		&StepCreateInstance{},
		&StepWaitRunning{},
		&StepResolveAddress{},
	)

	if useSSH {
		steps = append(steps,
			&communicator.StepConnect{
				Config: &b.config.Comm,
				Host: func(state multistep.StateBag) (string, error) {
					return state.Get("vm_ip").(string), nil
				},
				SSHConfig: b.config.Comm.SSHConfigFunc(),
			},
			&commonsteps.StepProvision{},
		)
	}

	// Shutdown + push only matter when a push target is configured.
	if b.config.PushImage != "" {
		steps = append(steps,
			&StepShutdown{},
			&StepPushImage{},
		)
	}

	b.runner = commonsteps.NewRunner(steps, b.config.PackerConfig, ui)
	b.runner.Run(ctx, state)

	if rawErr, ok := state.GetOk("error"); ok {
		return nil, rawErr.(error)
	}
	if _, ok := state.GetOk("instance_id"); !ok {
		return nil, errors.New("build did not produce an instance")
	}

	artifact := &Artifact{
		VMName:   b.config.Name,
		RegionID: b.config.RegionID,
	}
	if v, ok := state.GetOk("pushed_image"); ok {
		artifact.PushedImage = v.(string)
	}
	if v, ok := state.GetOk("pushed_digest"); ok {
		artifact.PushedDigest = v.(string)
	}
	if v, ok := state.GetOk("registered_image_name"); ok {
		artifact.RegisteredImageName = v.(string)
	}
	return artifact, nil
}
