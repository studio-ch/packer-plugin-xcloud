package builder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/hashicorp/packer-plugin-sdk/packer"

	"github.com/studio-ch/packer-plugin-xcloud/apiclient"
)

// agentCommunicator implements packer.Communicator by tunnelling every
// operation through the Cloud Console xcloud-agent exec/file API instead of
// SSH. Because the in-guest agent runs as root, provisioners run as root with
// no sudo and the VM needs no SSH reachability / public IP.
//
//   - Start     → POST /agent/exec (NDJSON stream) running `/bin/sh -c <cmd>`
//   - Upload    → POST /agent/files (raw body)
//   - UploadDir → recursive per-file Upload
//   - Download  → GET  /agent/files
//   - DownloadDir → unsupported in v1
//
// It is safe for concurrent use: the underlying *apiclient.Client issues an
// independent HTTP request per call.
type agentCommunicator struct {
	client     *apiclient.Client
	instanceID string
	// ctx is the build context, used by the blocking file operations
	// (Upload/Download) which the packer.Communicator interface does not
	// give their own context. Start uses the context it is handed.
	ctx context.Context
	// env is forwarded to every exec; nil for the shell provisioner, which
	// inlines its own environment into the command string.
	env map[string]string
}

var _ packer.Communicator = (*agentCommunicator)(nil)

// newAgentCommunicator builds an agent-backed communicator.
func newAgentCommunicator(ctx context.Context, client *apiclient.Client, instanceID string) *agentCommunicator {
	return &agentCommunicator{client: client, instanceID: instanceID, ctx: ctx}
}

// Start runs cmd.Command through `/bin/sh -c` inside the VM. It returns
// immediately; the command runs in a goroutine that streams stdout/stderr to
// the RemoteCmd writers and records the exit status via SetExited.
func (a *agentCommunicator) Start(ctx context.Context, cmd *packer.RemoteCmd) error {
	go func() {
		code, err := a.client.ExecStream(
			ctx,
			a.instanceID,
			[]string{"/bin/sh", "-c", cmd.Command},
			a.env,
			"",
			cmd.Stdout,
			cmd.Stderr,
		)
		if err != nil {
			// A transport / API failure (or an exit frame carrying an
			// error) must not look like success. Surface it and coerce a
			// non-positive code to a generic failure.
			if cmd.Stderr != nil {
				fmt.Fprintf(cmd.Stderr, "\nxcloud agent exec error: %v\n", err)
			}
			if code <= 0 {
				code = 1
			}
		}
		cmd.SetExited(code)
	}()
	return nil
}

// Upload writes the contents of r to dst inside the VM, honouring the file
// mode from fi when available.
func (a *agentCommunicator) Upload(dst string, r io.Reader, fi *os.FileInfo) error {
	var mode os.FileMode
	if fi != nil && *fi != nil {
		mode = (*fi).Mode().Perm()
	}
	return a.client.UploadFile(a.ctx, a.instanceID, dst, mode, r)
}

// UploadDir recursively uploads the contents of src to dst. It mirrors
// rsync(1) semantics: when src has no trailing slash, its basename directory
// is recreated under dst; with a trailing slash the contents land directly in
// dst. Files whose base name matches an entry in exclude are skipped.
func (a *agentCommunicator) UploadDir(dst string, src string, exclude []string) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat upload dir %q: %w", src, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("UploadDir source %q is not a directory", src)
	}

	baseDst := dst
	if !strings.HasSuffix(src, "/") {
		baseDst = path.Join(dst, filepath.Base(filepath.Clean(src)))
	}
	cleanSrc := filepath.Clean(src)

	excluded := func(name string) bool {
		for _, e := range exclude {
			if e == name {
				return true
			}
		}
		return false
	}

	return filepath.Walk(cleanSrc, func(p string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if excluded(fi.Name()) {
			if fi.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if fi.IsDir() {
			return nil
		}
		if !fi.Mode().IsRegular() {
			// Skip symlinks / devices / sockets — the agent file write
			// only handles plain regular files.
			return nil
		}
		rel, err := filepath.Rel(cleanSrc, p)
		if err != nil {
			return err
		}
		remote := path.Join(baseDst, filepath.ToSlash(rel))

		f, err := os.Open(p)
		if err != nil {
			return fmt.Errorf("open %q: %w", p, err)
		}
		defer f.Close()

		mode := fi.Mode().Perm()
		if err := a.client.UploadFile(a.ctx, a.instanceID, remote, mode, f); err != nil {
			return fmt.Errorf("upload %q -> %q: %w", p, remote, err)
		}
		return nil
	})
}

// Download streams the bytes of src inside the VM to w.
func (a *agentCommunicator) Download(src string, w io.Writer) error {
	return a.client.DownloadFile(a.ctx, a.instanceID, src, w)
}

// DownloadDir is not supported by the agent communicator in v1 — the agent
// exposes no directory-read RPC and per-file download is emulated via `cat`.
func (a *agentCommunicator) DownloadDir(src string, dst string, exclude []string) error {
	return errors.New("DownloadDir is not supported by the xcloud agent communicator")
}
