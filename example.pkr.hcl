packer {
  required_plugins {
    xcloud = {
      version = ">= 0.1.0"
      # Packer strips the "packer-plugin-" repo prefix from source addresses,
      # so this installs from github.com/studio-ch/packer-plugin-xcloud.
      source = "github.com/studio-ch/xcloud"
    }
  }
}

# Minimal macOS build:
#   1. pull a base OCI image into the tenant catalog (temporary)
#   2. boot a builder VM and reach it over SSH
#   3. run a provisioner
#   4. shut down and push the result as a new OCI image
#
# api_endpoint / api_token fall back to the CLOUD_CONSOLE_API_ENDPOINT and
# CLOUD_CONSOLE_API_TOKEN environment variables when omitted.

source "xcloud" "macos" {
  api_endpoint = "https://<your-cloud-console-host>"
  # api_token  = "..."   # prefer CLOUD_CONSOLE_API_TOKEN

  region_id = "00000000-0000-0000-0000-000000000000"
  name      = "packer-macos"

  cpu_cores = 4
  memory    = 8
  disk      = 64

  # Base image: pull a public OCI reference. Use `image = "<catalog-name>"`
  # instead to build from an existing catalog entry.
  pull_image = "ghcr.io/studio-ch/macos-sequoia:latest"

  # Reachability: allocate a public elastic IP for SSH (default true).
  use_elastic_ip = true

  # Communicator. When no ssh_key_ids are given the plugin generates an
  # ephemeral keypair, registers it, and tears it down on completion.
  communicator = "ssh"

  # Bring-your-own SSH key (optional). Register your own public key for the
  # build and authenticate with the matching private key via Packer's native
  # ssh_private_key_file. The registered key is deleted on cleanup. Leave both
  # unset to use the auto-generated ephemeral key above.
  # ssh_authorized_key   = file("~/.ssh/id_ed25519.pub")
  # ssh_private_key_file = "~/.ssh/id_ed25519"

  # Push target: the provisioned VM is shut down and pushed here.
  push_image    = "ghcr.io/studio-ch/macos-built:latest"
  push_username = "studio-ch"
  # push_password = "..."
  push_precache = false
}

build {
  sources = ["source.xcloud.macos"]

  provisioner "shell" {
    inline = [
      "echo 'provisioning the xcloud builder VM'",
      "sw_vers || uname -a",
    ]
  }
}

# ---------------------------------------------------------------------------
# Agent communicator variant (no SSH).
#
# `use_agent_communicator = true` runs provisioners *inside* the VM through the
# in-guest xcloud-agent (over the Cloud Console agent exec/file API) instead of
# SSH. Because the agent runs as root:
#   * no SSH reachability is required (no public/elastic IP, no port 22),
#   * no SSH key is generated or registered,
#   * provisioners run as root — no `sudo` and no sudo password.
#
# Requirements:
#   * the Cloud Console API exposes /v1/xcloud/instances/:id/agent/exec + /files
#   * the tenant has the agent-exec entitlement enabled
#     (tenants.xcloud_agent_exec_enabled = true) — ask an operator.
#
# In this mode the plugin forces the communicator to "none", defaults
# use_elastic_ip to false, and waits for the agent to report healthy before
# provisioning.

source "xcloud" "macos_agent" {
  api_endpoint = "https://<your-cloud-console-host>"
  # api_token  = "..."   # prefer CLOUD_CONSOLE_API_TOKEN

  region_id = "00000000-0000-0000-0000-000000000000"
  name      = "packer-macos-agent"

  cpu_cores = 4
  memory    = 8
  disk      = 64

  image = "macos-tahoe-agent"

  # Run provisioners via the in-guest agent (root, no SSH, no public IP).
  use_agent_communicator = true
}

build {
  sources = ["source.xcloud.macos_agent"]

  provisioner "shell" {
    inline = [
      "whoami",          # -> root
      "sw_vers || uname -a",
    ]
  }
}
