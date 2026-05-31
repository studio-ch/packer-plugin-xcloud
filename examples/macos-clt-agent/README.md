# macOS Command Line Tools (agent communicator)

Headless build that installs the Xcode Command Line Tools into a macOS image
using the **agent communicator** — provisioners run *inside* the VM via the
in-guest xcloud-agent (as root, no SSH, no public IP).

## Files

- [`macos-clt.pkr.hcl`](./macos-clt.pkr.hcl) — the build template. Uses the
  default agent communicator (`use_agent_communicator` defaults to `true` since
  v0.3.1).
- [`install-clt.sh`](./install-clt.sh) — the provisioner script that installs
  the Command Line Tools headless via `softwareupdate`.

## Requirements

- An image that ships the xcloud-agent (e.g. `macos-tahoe-agent`).
- The Cloud Console agent-exec API and the tenant's
  `xcloud_agent_exec_enabled` entitlement (ask a platform operator).

## Run

No secrets live in the template — provide them via the environment:

```bash
export CLOUD_CONSOLE_API_ENDPOINT="https://<your-cloud-console-host>"
export CLOUD_CONSOLE_API_TOKEN="<your-api-key>"

# Edit region_id in macos-clt.pkr.hcl to your BIT1 region UUID first.
packer init .
packer build macos-clt.pkr.hcl
```
