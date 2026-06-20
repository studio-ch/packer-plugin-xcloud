###############################################################################
# macOS Command Line Tools build — agent communicator + image push
#
# What this template does, end to end:
#   1. Creates a temporary macOS VM from a base image on your Cloud Console
#      tenant (via the xcloud builder).
#   2. Runs `install-clt.sh` INSIDE the guest as root through the in-guest
#      xcloud agent (no SSH, no public IP needed).
#   3. Stops the VM and PUSHES it as an OCI image to your registry, then
#      registers it in your tenant image catalog.
#   4. Tears the temporary VM (and any temp resources) down again.
#
# Nothing secret lives in this file — everything sensitive comes from the
# environment (see the `variable` blocks and the env vars below).
###############################################################################

packer {
  required_plugins {
    xcloud = {
      # Pin a floor version; image push (push_image) needs >= 0.3.3.
      version = ">= 0.3.3"
      source  = "github.com/studio-ch/xcloud"
    }
  }
}

###############################################################################
# Inputs (provided via environment variables; no secrets in the template)
#
#   export CLOUD_CONSOLE_API_ENDPOINT="https://<your-cloud-console-host>"
#   export CLOUD_CONSOLE_API_TOKEN="<tenant API key with write:resources>"
#   export XCLOUD_PUSH_IMAGE="ghcr.io/<org>/<repo>:<tag>"
#   export XCLOUD_PUSH_CREDENTIAL_ID="<saved registry credential UUID>"
#
# api_endpoint / api_token are read from CLOUD_CONSOLE_* by the plugin itself,
# so they don't need a variable here. The two push inputs below use Packer's
# env() so they can also be overridden with -var on the command line.
###############################################################################

variable "push_image" {
  type        = string
  default     = env("XCLOUD_PUSH_IMAGE")
  description = <<-EOT
    OCI reference to push the finished image to, e.g.
    "ghcr.io/acme/macos-tahoe-clt:latest". Leave empty to skip the push and
    only build the VM (useful while iterating on the provisioner).
  EOT
}

variable "push_credential_id" {
  type        = string
  default     = env("XCLOUD_PUSH_CREDENTIAL_ID")
  description = <<-EOT
    UUID of a registry credential you saved in Cloud Console
    (Integration -> Registries). The push authenticates with this saved
    credential. Leave empty to fall back to ad-hoc creds (push_username /
    push_password) or an anonymous push (public repos only).

    Find the UUID in the panel: Integration -> Registries -> row kebab
    "Copy ID" (or the "Credential ID" field in the edit dialog). Or via API:

      curl -s -H "Authorization: Bearer $CLOUD_CONSOLE_API_TOKEN" \
        "$CLOUD_CONSOLE_API_ENDPOINT/v1/registry-credentials" \
        | jq '.data[] | {id, displayName, registryUrl}'

    Pick the id whose registryUrl matches the host in push_image.
  EOT
}

source "xcloud" "clt" {
  # ---- Connection -----------------------------------------------------------
  # api_endpoint / api_token are taken from CLOUD_CONSOLE_API_ENDPOINT /
  # CLOUD_CONSOLE_API_TOKEN in the environment. You can also set them inline:
  #   api_endpoint = "https://cloud.example.com"
  #   api_token    = "sk_live_..."   # discouraged — prefer the env var

  # ---- Where + what to build ------------------------------------------------
  region = "<region>"        # e.g. ZRH1, ALP2; or use region_id = "<uuid>"
  image  = "macos-tahoe-agent" # a base image that ships the xcloud agent

  # Optional instance sizing (defaults shown). Bigger disks take longer to push.
  # cpu_cores = 4
  # memory    = 8     # GiB
  # disk      = 64    # GiB

  # ---- How provisioners run -------------------------------------------------
  # use_agent_communicator defaults to true since v0.3.1: provisioners run
  # in-guest via the xcloud agent as root (no SSH key, no elastic IP, no sudo
  # password). Set it to false to opt back into the classic SSH path.
  # use_agent_communicator = true

  # ---- Timeouts -------------------------------------------------------------
  # state_timeout bounds each long-running step, INCLUDING the image push.
  # macOS images are large (tens of GB), so the push alone can take many
  # minutes — give it generous headroom (match worker PUSH_TIMEOUT_MS, 2h).
  state_timeout = "2h"

  # ---- Image push (optional) ------------------------------------------------
  # After the provisioners finish, the builder stops the VM and pushes it as an
  # OCI image to push_image, then registers it in your tenant image catalog.
  # Leave push_image empty (the default) to skip this entirely.
  push_image         = var.push_image
  push_credential_id = var.push_credential_id

  # Credential precedence for the push (the plugin picks the first that is set):
  #   1. push_credential_id           -> a credential saved in Cloud Console
  #   2. push_username + push_password -> ad-hoc creds (e.g. a GHCR PAT)
  #   3. (neither)                     -> anonymous push (public repos only)
  #
  # Ad-hoc alternative instead of push_credential_id:
  #   push_username = "acme-ci"
  #   push_password = env("GHCR_PAT")
  #
  # push_precache = false   # set true to eagerly pull the pushed image onto
  #                         # every node in the region (faster first boot,
  #                         # more disk usage).
}

build {
  sources = ["source.xcloud.clt"]

  provisioner "shell" {
    # Runs inside the guest (as root via the agent). Installs the Xcode
    # Command Line Tools headless; can take a while on a cold image.
    script  = "install-clt.sh"
    timeout = "40m"
  }
}
