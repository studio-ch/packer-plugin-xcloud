packer {
  required_plugins {
    xcloud = {
      version = ">= 0.3.3"
      source  = "github.com/studio-ch/xcloud"
    }
  }
}

# Set CLOUD_CONSOLE_API_ENDPOINT and CLOUD_CONSOLE_API_TOKEN in the environment.
source "xcloud" "clt" {
  region = "BIT1" # friendly region slug (or set region_id = "<uuid>" instead)
  image  = "macos-tahoe-agent"
  # use_agent_communicator defaults to true: provisioners run in-guest via the
  # xcloud agent as root (no SSH, no elastic IP). Requires the agent-exec API
  # + the tenant's xcloud_agent_exec_enabled entitlement.
}

build {
  sources = ["source.xcloud.clt"]

  provisioner "shell" {
    script = "install-clt.sh"
  }
}
