# Cloud Console Packer Plugin

A [HashiCorp Packer](https://www.packer.io) builder plugin for creating custom
**macOS virtual machine images on Xcloud**. It lets you bake your own base
images — pre-installed tools, Xcode versions, CI runners, certificates — and
publish them straight into your Cloud Console image catalog.

The plugin talks to the public Cloud Console API with your API key: it
provisions a builder VM, runs your provisioners, shuts the VM down, and pushes
the result as a reusable OCI image. No VPN, certificates, or low-level cluster
access required.

> **Default since v0.3.1:** provisioners run **in-guest via the xcloud-agent**
> (`use_agent_communicator` defaults to `true`) — no SSH, no public IP, runs as
> root. This requires the Cloud Console agent-exec API and the tenant's
> `xcloud_agent_exec_enabled` entitlement. Set `use_agent_communicator = false`
> to use the classic SSH path instead. See
> [Agent communicator](#agent-communicator-no-ssh).

## Install

Add the plugin to your template and let Packer fetch it:

```hcl
packer {
  required_plugins {
    xcloud = {
      version = ">= 0.1.0"
      # Packer source addresses omit the "packer-plugin-" repo prefix.
      # This resolves to the repo github.com/studio-ch/packer-plugin-xcloud.
      source = "github.com/studio-ch/xcloud"
    }
  }
}
```

```bash
packer init .
```

Or build and install it manually:

```bash
go install github.com/studio-ch/packer-plugin-xcloud@latest
# or, for a local checkout:
go build -o ~/.packer.d/plugins/packer-plugin-xcloud .
```

## Authentication

Create an API key in Cloud Console (with the `write:resources` scope and the
Xcloud service enabled), then provide it to the plugin. The key is sent as
`Authorization: Bearer <token>`.

| Config field   | Environment fallback     |
| -------------- | ------------------------ |
| `api_endpoint` | `CLOUD_CONSOLE_API_ENDPOINT` |
| `api_token`    | `CLOUD_CONSOLE_API_TOKEN`    |

## Quick start

See [`example.pkr.hcl`](./example.pkr.hcl) for a complete macOS build. The
short version:

```hcl
source "xcloud" "macos" {
  region_id  = "<your-region-uuid>"
  pull_image = "ghcr.io/your-org/macos-base:latest"  # base to build from
  push_image = "ghcr.io/your-org/macos-built:latest" # where the result lands
}

build {
  sources = ["source.xcloud.macos"]

  provisioner "shell" {
    inline = ["sw_vers", "echo 'baking the image'"]
  }
}
```

```bash
export CLOUD_CONSOLE_API_ENDPOINT="https://<your-cloud-console-host>"
export CLOUD_CONSOLE_API_TOKEN="<your-api-key>"
packer build example.pkr.hcl
```

## Configuration

| Field                | Type      | Default          | Notes |
| -------------------- | --------- | ---------------- | ----- |
| `api_endpoint`       | string    | — (required)     | Cloud Console API host, with or without scheme. |
| `api_token`          | string    | — (required)     | Your API key. |
| `region_id`          | string    | — (required)     | Region UUID. |
| `name`               | string    | `packer-<8hex>`  | Builder VM name. |
| `cpu_cores`          | int       | `4`              | |
| `memory`             | int       | `8`              | GiB. |
| `disk`               | int       | `64`             | GiB. |
| `network`            | string    | `default`        | Network name to attach. |
| `image`              | string    | —                | Existing catalog image (mutually exclusive with `pull_image`). |
| `pull_image`         | string    | —                | OCI reference to register before the build (removed on cleanup). |
| `pull_username`      | string    | —                | Registry username for `pull_image`. |
| `pull_password`      | string    | —                | Registry password for `pull_image`. |
| `pull_credential_id` | string    | —                | Saved registry credential id (alternative to user/pass). |
| `pull_precache`      | bool      | `false`          | |
| `admin_username`     | string    | server-resolved  | SSH login user. At run time it is resolved from the server/image label, falling back to this value, then `admin`. Sets the SSH communicator username (use `ssh_username` to override). |
| `ssh_key_ids`        | list      | —                | Existing (pre-registered) SSH key ids to attach. When empty, the plugin registers a key for the build (see `ssh_authorized_key` / ephemeral below). |
| `ssh_authorized_key` | string    | —                | Bring-your-own OpenSSH **public** key (e.g. `ssh-ed25519 AAAA...`). Registered as a tenant key for the build, attached to the VM, then deleted on cleanup (unless `keep_vm`). No private key is generated — pair it with the native `ssh_private_key_file` so the communicator can authenticate. |
| `use_elastic_ip`     | bool      | `true`*          | Allocate a public IP for SSH; otherwise use the private address. *Defaults to `false` when `use_agent_communicator` is on (the default). |
| `use_agent_communicator` | bool  | `true`           | Run provisioners in-guest via the xcloud-agent instead of SSH (no SSH, no public IP, runs as root). **Defaults to true** — requires the Cloud Console agent-exec API and the tenant `xcloud_agent_exec_enabled` entitlement. Set to `false` for the SSH path. See [Agent communicator](#agent-communicator-no-ssh). |
| `push_image`         | string    | —                | OCI reference to push the finished image to. |
| `push_username`      | string    | —                | |
| `push_password`      | string    | —                | |
| `push_credential_id` | string    | —                | Saved registry credential id. |
| `push_precache`      | bool      | `false`          | Pre-pull the pushed image onto every node for faster first boot. |
| `keep_vm`            | bool      | `false`          | Skip teardown of the VM and temporary resources. |
| `poll_interval`      | duration  | `5s`             | |
| `state_timeout`      | duration  | `20m`            | |
| `communicator`       | string    | `none`*          | Only `ssh` or `none`. Forced to `none` when `use_agent_communicator` is on (the default). *Defaults to `ssh` when `use_agent_communicator = false`. |

## How a build runs

The steps below describe the **SSH path** (`use_agent_communicator = false`).
With the default agent communicator, steps 2 and 6–7 differ — see
[Agent communicator](#agent-communicator-no-ssh).

1. **Register image** — register `pull_image` as a temporary catalog image
   (skipped when `image` is used).
2. **SSH key** — when `ssh_key_ids` is empty the plugin registers a key for
   the build. If `ssh_authorized_key` is set, that bring-your-own public key is
   registered (pair it with the native `ssh_private_key_file`); otherwise, if
   no native `ssh_private_key_file` was supplied, an ephemeral ed25519 keypair
   is generated and registered. The registered key is deleted on cleanup.
3. **Create network** — optional (off by default; uses `network`).
4. **Create instance** — the builder VM is created and started.
5. **Wait running** — poll until the VM is running and ready.
6. **Resolve address** — wait for the elastic IP to bind, or use the private
   address.
7. **Connect + provision** — Packer's SSH communicator runs your provisioners.
8. **Shutdown + push** — the VM is shut down gracefully and pushed as an OCI
   image (only when `push_image` is set).

All resources created during the build (VM, elastic IP, temporary image and
network, the registered SSH key — whether ephemeral or a bring-your-own
`ssh_authorized_key`) are cleaned up automatically unless `keep_vm` is set.

## Bring your own SSH key

Instead of letting the plugin generate an ephemeral keypair, you can register
your own public key for the build and authenticate with the matching private
key via Packer's native `ssh_private_key_file`:

```hcl
source "xcloud" "macos" {
  region_id  = "<your-region-uuid>"
  pull_image = "ghcr.io/your-org/macos-base:latest"

  # Register this public key for the build (deleted again on cleanup).
  ssh_authorized_key   = file("~/.ssh/id_ed25519.pub")
  # Authenticate with the matching private key (Packer's native option).
  ssh_private_key_file = "~/.ssh/id_ed25519"
}
```

The key is registered before the VM is created and torn down on completion
(unless `keep_vm = true`). This is independent of `ssh_key_ids` (already
pre-registered tenant keys) — set one or the other.

## Agent communicator (no SSH)

This is the **default** (since v0.3.1): provisioners run *inside* the VM through
the in-guest **xcloud-agent** (over the Cloud Console agent exec/file API)
instead of SSH. The `shell` and `file` provisioners work unchanged — Packer
calls the agent-backed communicator rather than SSH. Because it is the default,
`use_agent_communicator = true` does not need to be set explicitly; set
`use_agent_communicator = false` to opt back into SSH.

See [`examples/macos-clt-agent`](./examples/macos-clt-agent) for a complete
example that installs the Xcode Command Line Tools headless through the agent.

Because the agent runs as **root**:

- **No SSH** — no port 22, no SSH key generated or registered, no
  `ssh_private_key_file`. The communicator is forced to `none`.
- **No public IP** — the agent is reached over the Cloud Console gateway, not
  the VM's network, so `use_elastic_ip` defaults to `false`.
- **No sudo** — provisioners already run as root, so there is no sudo-password
  problem.

```hcl
source "xcloud" "macos" {
  region_id = "<your-region-uuid>"
  image     = "macos-tahoe-agent"

  use_agent_communicator = true
}

build {
  sources = ["source.xcloud.macos"]
  provisioner "shell" {
    inline = ["whoami", "sw_vers"]   # whoami -> root
  }
}
```

### Requirements

- The Cloud Console API must expose the agent endpoints
  `POST /v1/xcloud/instances/:id/agent/exec` and
  `POST|GET /v1/xcloud/instances/:id/agent/files`.
- The tenant must have the **agent-exec entitlement** enabled
  (`tenants.xcloud_agent_exec_enabled = true`). Ask a platform operator to flip
  it; until then the build fails fast with a `403 agent_exec_disabled` while
  connecting the agent.

### How it differs from the SSH flow

After **Wait running**, instead of resolving an address and connecting over
SSH, the plugin runs a **Connect agent** step that polls a cheap no-op exec
until the agent reports ready (retrying while the API returns
`409 agent_not_ready`, up to `state_timeout`), then provisions via the agent.

### Limitations

- `Download` of a single file is supported but **capped** (the API emulates it
  via `cat`; large files return `413`). `DownloadDir` is **not supported** in
  v1.
- There is no interactive stdin; provisioners that expect a TTY won't work.
