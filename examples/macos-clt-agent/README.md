# macOS Command Line Tools (agent communicator + image push)

Headless build that installs the Xcode Command Line Tools into a macOS image
using the **agent communicator** — provisioners run *inside* the VM via the
in-guest xcloud-agent (as root, no SSH, no public IP) — and then **pushes the
finished image** to your OCI registry.

## Files

- [`macos-clt.pkr.hcl`](./macos-clt.pkr.hcl) — the build template. Uses the
  default agent communicator (`use_agent_communicator` defaults to `true` since
  v0.3.1) and an optional image-push step.
- [`install-clt.sh`](./install-clt.sh) — the provisioner script that installs
  the Command Line Tools headless via `softwareupdate`.

## What it does

1. Creates a temporary macOS VM from the base `image` on your tenant.
2. Runs `install-clt.sh` in-guest as root via the agent.
3. Stops the VM and pushes it as an OCI image to `push_image`, then registers
   it in your tenant image catalog.
4. Tears the temporary VM down again.

Set `push_image` to empty to skip step 3 (build only).

## Requirements

- An image that ships the xcloud-agent (e.g. `macos-tahoe-agent`).
- A tenant API key with the `write:resources` scope.
- For a **saved-credential** push: a registry credential configured in
  Cloud Console under **Integration -> Registries**.

## Run

No secrets live in the template — provide them via the environment:

```bash
export CLOUD_CONSOLE_API_ENDPOINT="https://<your-cloud-console-host>"
export CLOUD_CONSOLE_API_TOKEN="<your-api-key>"           # write:resources

# Where to push the finished image, and which saved credential to auth with:
export XCLOUD_PUSH_IMAGE="ghcr.io/<org>/<repo>:<tag>"
export XCLOUD_PUSH_CREDENTIAL_ID="<saved registry credential UUID>"

# Edit `region` (or set region_id) in macos-clt.pkr.hcl to your region first.
packer init .
packer build macos-clt.pkr.hcl
```

## Registry credentials for the push

The push picks the first auth method that is set:

1. **Saved credential (recommended)** — `push_credential_id` = the UUID of a
   credential saved in Cloud Console (**Integration -> Registries**).
2. **Ad-hoc** — `push_username` + `push_password` (e.g. a GHCR PAT). Uncomment
   the relevant lines in the template.
3. **Anonymous** — neither set; only works for public repositories.

### Finding a saved credential's UUID

- In the panel: **Integration -> Registries**, then the row's kebab menu
  -> **Copy ID** (or the **Credential ID** field in the edit dialog).
- Or via the API:

  ```bash
  curl -s -H "Authorization: Bearer $CLOUD_CONSOLE_API_TOKEN" \
    "$CLOUD_CONSOLE_API_ENDPOINT/v1/registry-credentials" \
    | jq '.data[] | {id, displayName, registryUrl}'
  ```

  Use the `id` whose `registryUrl` matches the host in `push_image`
  (e.g. `https://ghcr.io` for a `ghcr.io/...` image).

## Notes

- macOS images are large (tens of GB); the push can take many minutes.
  `state_timeout` in the template bounds it — keep it generous (e.g. `2h` for large macOS pushes).
- `push_credential_id` must point at a credential for the **same registry host**
  as `push_image`, otherwise the registry rejects the auth.
