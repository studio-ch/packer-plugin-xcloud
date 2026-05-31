// Package apiclient is a thin REST client for the xcloud tenant API.
//
// It speaks the public /v1 surface with a Bearer API key. It intentionally
// has no dependency on the upstream xcloud Go module, gRPC, mTLS or proto:
// every call is a plain JSON HTTP request. Non-2xx responses are parsed as
// RFC 7807 ProblemDetails and surfaced as a typed *APIError.
package apiclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Client is a tenant-scoped xcloud REST client.
type Client struct {
	endpoint string
	token    string
	hc       *http.Client
	// streamHC is used for the long-lived / large agent transfers
	// (exec stream, file upload/download). It shares the transport of
	// hc but carries NO request timeout — a `shell` provisioner can
	// run for minutes (e.g. an OS update) and must not be cut off by
	// the default 60s deadline.
	streamHC *http.Client
}

// New builds a client for the given API endpoint host and Bearer token. The
// endpoint may be supplied with or without a trailing slash and with or
// without a scheme (defaults to https). httpClient is optional; a sane
// default with a 60s timeout is used when nil.
func New(endpoint, token string, httpClient *http.Client) *Client {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint != "" && !strings.Contains(endpoint, "://") {
		endpoint = "https://" + endpoint
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}
	streamHC := &http.Client{Transport: httpClient.Transport}
	return &Client{endpoint: endpoint, token: token, hc: httpClient, streamHC: streamHC}
}

// APIError is a typed error parsed from an RFC 7807 ProblemDetails body.
type APIError struct {
	Status int    `json:"status"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Type   string `json:"type"`
}

func (e *APIError) Error() string {
	msg := e.Detail
	if msg == "" {
		msg = e.Title
	}
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	return fmt.Sprintf("xcloud API error %d: %s", e.Status, msg)
}

// doJSON performs a request against path (relative to the /v1 prefix),
// marshalling body (when non-nil) and decoding the response into out (when
// non-nil). On a non-2xx status it parses ProblemDetails into *APIError.
func (c *Client) doJSON(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		reader = bytes.NewReader(buf)
	}

	u := c.endpoint + "/v1" + path
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		apiErr := &APIError{Status: resp.StatusCode}
		if len(raw) > 0 {
			// Best-effort ProblemDetails parse; fall back to raw text.
			_ = json.Unmarshal(raw, apiErr)
			if apiErr.Detail == "" && apiErr.Title == "" {
				apiErr.Detail = strings.TrimSpace(string(raw))
			}
		}
		if apiErr.Status == 0 {
			apiErr.Status = resp.StatusCode
		}
		return apiErr
	}

	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

/* ============================ Auth union ============================ */

// RegistryAuth is the discriminated registry-credential union shared by the
// image-register and image-push request bodies. It marshals to one of:
//
//	{"kind":"saved","credentialId":"<uuid>"}
//	{"kind":"adhoc","username":"<u>","password":"<p>"}
//	{"kind":"anonymous"}
type RegistryAuth struct {
	Kind         string
	CredentialID string
	Username     string
	Password     string
}

// AnonymousAuth returns an anonymous (public registry) credential union.
func AnonymousAuth() RegistryAuth { return RegistryAuth{Kind: "anonymous"} }

// SavedAuth returns a saved-credential union referencing a tenant credential id.
func SavedAuth(credentialID string) RegistryAuth {
	return RegistryAuth{Kind: "saved", CredentialID: credentialID}
}

// AdhocAuth returns an ad-hoc username/password credential union.
func AdhocAuth(username, password string) RegistryAuth {
	return RegistryAuth{Kind: "adhoc", Username: username, Password: password}
}

func (a RegistryAuth) MarshalJSON() ([]byte, error) {
	switch a.Kind {
	case "saved":
		return json.Marshal(struct {
			Kind         string `json:"kind"`
			CredentialID string `json:"credentialId"`
		}{"saved", a.CredentialID})
	case "adhoc":
		return json.Marshal(struct {
			Kind     string `json:"kind"`
			Username string `json:"username"`
			Password string `json:"password"`
		}{"adhoc", a.Username, a.Password})
	default:
		return json.Marshal(struct {
			Kind string `json:"kind"`
		}{"anonymous"})
	}
}

/* ============================ DTOs ============================ */

// Image is the registered-image DTO returned by POST /v1/xcloud/images.
type Image struct {
	Name         string            `json:"name"`
	RegionID     string            `json:"regionId"`
	RegionSlug   string            `json:"regionSlug"`
	Source       string            `json:"source"`
	OCIReference string            `json:"ociReference"`
	Labels       map[string]string `json:"labels"`
	CreatedAt    *string           `json:"createdAt"`
}

// Instance is the (subset of the) instance DTO the builder polls on.
type Instance struct {
	ID             string  `json:"id"`
	Status         string  `json:"status"`
	PendingAction  *string `json:"pendingAction"`
	NetworkAddress *string `json:"networkAddress"`
	LastError      *string `json:"lastError"`
	RegionID       string  `json:"regionId"`
	RegionSlug     string  `json:"regionSlug"`
	AdminUsername  *string `json:"adminUsername"`
}

// ElasticIP is the (subset of the) elastic-ip DTO the builder polls on.
type ElasticIP struct {
	ID               string  `json:"id"`
	PublicAddress    *string `json:"publicAddress"`
	Status           string  `json:"status"`
	TargetInstanceID *string `json:"targetInstanceId"`
	UpstreamBound    bool    `json:"upstreamBound"`
}

// PushJob is the (subset of the) image-push job DTO the builder polls on.
type PushJob struct {
	ID                  string  `json:"id"`
	Status              string  `json:"status"`
	Digest              *string `json:"digest"`
	Error               *string `json:"error"`
	Precache            bool    `json:"precache"`
	RegisteredImageName *string `json:"registeredImageName"`
	RegisteredAt        *string `json:"registeredAt"`
}

// SSHKey is the (subset of the) ssh-key DTO returned by POST /v1/ssh-keys.
type SSHKey struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	FingerprintSha256 string `json:"fingerprintSha256"`
}

/* ============================ Images ============================ */

// RegisterImageRequest is the body for POST /v1/xcloud/images.
type RegisterImageRequest struct {
	RegionID     string       `json:"regionId"`
	Name         string       `json:"name"`
	OCIReference string       `json:"ociReference"`
	Auth         RegistryAuth `json:"auth"`
	Precache     bool         `json:"precache,omitempty"`
}

// RegisterImage registers an OCI image in the tenant catalog.
func (c *Client) RegisterImage(ctx context.Context, req RegisterImageRequest) (*Image, error) {
	var out Image
	if err := c.doJSON(ctx, http.MethodPost, "/xcloud/images", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteImage removes a tenant-namespace image by region + name.
func (c *Client) DeleteImage(ctx context.Context, regionID, name string) error {
	path := fmt.Sprintf("/xcloud/images/%s/%s", url.PathEscape(regionID), url.PathEscape(name))
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}

/* ============================ Instances ============================ */

// CreateInstanceRequest is the body for POST /v1/xcloud/instances.
type CreateInstanceRequest struct {
	RegionID         string            `json:"regionId"`
	Name             string            `json:"name"`
	CPUCores         int               `json:"cpuCores"`
	MemoryGib        int               `json:"memoryGib"`
	DiskGib          int               `json:"diskGib"`
	ImageRef         string            `json:"imageRef"`
	NetworkRef       string            `json:"networkRef,omitempty"`
	DisplayWidth     int               `json:"displayWidth,omitempty"`
	DisplayHeight    int               `json:"displayHeight,omitempty"`
	PendingElasticIP *PendingElasticIP `json:"pendingElasticIp,omitempty"`
	FlavorSlug       string            `json:"flavorSlug,omitempty"`
	AdminUsername    string            `json:"adminUsername,omitempty"`
	SSHKeyIDs        []string          `json:"sshKeyIds,omitempty"`
}

// PendingElasticIP is the deferred public-IP directive on instance create.
// Mode is "allocate" or "existing"; ExistingID is required for "existing".
type PendingElasticIP struct {
	Mode       string `json:"mode"`
	ExistingID string `json:"existingId,omitempty"`
}

// CreateInstance creates a tenant instance (status begins "pending").
func (c *Client) CreateInstance(ctx context.Context, req CreateInstanceRequest) (*Instance, error) {
	var out Instance
	if err := c.doJSON(ctx, http.MethodPost, "/xcloud/instances", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetInstance reads one instance by id.
func (c *Client) GetInstance(ctx context.Context, id string) (*Instance, error) {
	var out Instance
	path := "/xcloud/instances/" + url.PathEscape(id)
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ShutdownInstance queues a graceful ACPI shutdown.
func (c *Client) ShutdownInstance(ctx context.Context, id string) error {
	path := "/xcloud/instances/" + url.PathEscape(id) + "/shutdown"
	return c.doJSON(ctx, http.MethodPost, path, nil, nil)
}

// StopInstance queues a hard stop.
func (c *Client) StopInstance(ctx context.Context, id string) error {
	path := "/xcloud/instances/" + url.PathEscape(id) + "/stop"
	return c.doJSON(ctx, http.MethodPost, path, nil, nil)
}

// DeleteInstance async-deletes an instance.
func (c *Client) DeleteInstance(ctx context.Context, id string) error {
	path := "/xcloud/instances/" + url.PathEscape(id)
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}

/* ============================ Networks ============================ */

// CreateNetworkRequest is the body for POST /v1/xcloud/networks.
type CreateNetworkRequest struct {
	RegionID string            `json:"regionId"`
	Name     string            `json:"name"`
	Mode     string            `json:"mode,omitempty"`
	CIDR     string            `json:"cidr,omitempty"`
	Gateway  string            `json:"gateway,omitempty"`
	DHCP     *bool             `json:"dhcp,omitempty"`
	Spec     map[string]any    `json:"spec,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
}

// CreateNetwork creates a tenant network. The response body is not modelled —
// the builder only needs the name + region it already holds.
func (c *Client) CreateNetwork(ctx context.Context, req CreateNetworkRequest) error {
	return c.doJSON(ctx, http.MethodPost, "/xcloud/networks", req, nil)
}

// DeleteNetwork removes a tenant network by name + region.
func (c *Client) DeleteNetwork(ctx context.Context, regionID, name string) error {
	path := fmt.Sprintf("/xcloud/networks/%s?regionId=%s", url.PathEscape(name), url.QueryEscape(regionID))
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}

/* ============================ Elastic IPs ============================ */

// AllocateElasticIPRequest is the body for POST /v1/xcloud/elastic-ips.
type AllocateElasticIPRequest struct {
	RegionID   string `json:"regionId"`
	InstanceID string `json:"instanceId,omitempty"`
}

// AllocateElasticIP allocates an elastic IP, optionally attaching it to an
// instance on create.
func (c *Client) AllocateElasticIP(ctx context.Context, req AllocateElasticIPRequest) (*ElasticIP, error) {
	var out ElasticIP
	if err := c.doJSON(ctx, http.MethodPost, "/xcloud/elastic-ips", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListElasticIPsByInstance lists elastic IPs targeting the given instance.
func (c *Client) ListElasticIPsByInstance(ctx context.Context, instanceID string) ([]ElasticIP, error) {
	var out struct {
		Data []ElasticIP `json:"data"`
	}
	path := "/xcloud/elastic-ips?instanceId=" + url.QueryEscape(instanceID)
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// ReleaseElasticIP releases an elastic IP by id.
func (c *Client) ReleaseElasticIP(ctx context.Context, id string) error {
	path := "/xcloud/elastic-ips/" + url.PathEscape(id)
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}

/* ============================ SSH keys ============================ */

// CreateSSHKeyRequest is the body for POST /v1/ssh-keys.
type CreateSSHKeyRequest struct {
	Name      string `json:"name"`
	PublicKey string `json:"publicKey"`
}

// CreateSSHKey registers a tenant SSH public key.
func (c *Client) CreateSSHKey(ctx context.Context, req CreateSSHKeyRequest) (*SSHKey, error) {
	var out SSHKey
	if err := c.doJSON(ctx, http.MethodPost, "/ssh-keys", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteSSHKey removes a tenant SSH public key by id.
func (c *Client) DeleteSSHKey(ctx context.Context, id string) error {
	path := "/ssh-keys/" + url.PathEscape(id)
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}

/* ============================ Image push ============================ */

// CreatePushJobRequest is the body for POST /v1/xcloud/instances/{id}/push-image.
type CreatePushJobRequest struct {
	OCIReference string       `json:"ociReference"`
	Auth         RegistryAuth `json:"auth"`
	Precache     bool         `json:"precache,omitempty"`
}

// CreatePushJob enqueues an image-push job for a stopped instance.
func (c *Client) CreatePushJob(ctx context.Context, instanceID string, req CreatePushJobRequest) (*PushJob, error) {
	var out PushJob
	path := "/xcloud/instances/" + url.PathEscape(instanceID) + "/push-image"
	if err := c.doJSON(ctx, http.MethodPost, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetPushJob reads one image-push job by id.
func (c *Client) GetPushJob(ctx context.Context, jobID string) (*PushJob, error) {
	var out PushJob
	path := "/xcloud/image-pushes/" + url.PathEscape(jobID)
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

/* ============================ Agent exec / files ============================ */

// execRequest is the body for POST /v1/xcloud/instances/{id}/agent/exec.
type execRequest struct {
	Argv      []string          `json:"argv"`
	Env       map[string]string `json:"env,omitempty"`
	Cwd       string            `json:"cwd,omitempty"`
	TimeoutMs int64             `json:"timeoutMs,omitempty"`
}

// parseAPIErrorResponse parses a non-2xx response into an *APIError, draining
// and closing the body. Used by the streaming endpoints which bypass doJSON.
func parseAPIErrorResponse(resp *http.Response) *APIError {
	raw, _ := io.ReadAll(resp.Body)
	apiErr := &APIError{Status: resp.StatusCode}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, apiErr)
		if apiErr.Detail == "" && apiErr.Title == "" {
			apiErr.Detail = strings.TrimSpace(string(raw))
		}
	}
	if apiErr.Status == 0 {
		apiErr.Status = resp.StatusCode
	}
	return apiErr
}

// ExecStream runs argv inside the instance via the in-guest xcloud-agent and
// streams stdout/stderr to the given writers as the command produces them. It
// consumes the chunked `application/x-ndjson` response, base64-decoding each
// {"stream":...,"data":...} frame into the matching writer, and returns the
// exit code carried by the terminal {"exit":{"code":...}} frame.
//
// A pre-stream failure (auth / 404 / 409 / 403 / 422 / 5xx) is returned as an
// *APIError before any bytes are written. A non-empty exit error or a stream
// that ends without an exit frame is returned as a plain error alongside the
// best-known exit code.
func (c *Client) ExecStream(
	ctx context.Context,
	instanceID string,
	argv []string,
	env map[string]string,
	cwd string,
	stdout, stderr io.Writer,
) (int, error) {
	buf, err := json.Marshal(execRequest{Argv: argv, Env: env, Cwd: cwd})
	if err != nil {
		return -1, fmt.Errorf("marshal exec request: %w", err)
	}
	u := c.endpoint + "/v1/xcloud/instances/" + url.PathEscape(instanceID) + "/agent/exec"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(buf))
	if err != nil {
		return -1, fmt.Errorf("build exec request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/x-ndjson")

	resp, err := c.streamHC.Do(req)
	if err != nil {
		return -1, fmt.Errorf("POST agent/exec: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return -1, parseAPIErrorResponse(resp)
	}

	type execFrame struct {
		Stream string `json:"stream"`
		Data   string `json:"data"`
		Exit   *struct {
			Code     int    `json:"code"`
			TimedOut bool   `json:"timedOut"`
			Error    string `json:"error"`
		} `json:"exit"`
	}

	exitCode := -1
	var exitErr string
	sawExit := false

	// bufio.Reader.ReadBytes grows to fit arbitrarily long lines — a
	// single base64 chunk can be larger than the default scanner cap.
	br := bufio.NewReaderSize(resp.Body, 64*1024)
	for {
		line, readErr := br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var frame execFrame
			if err := json.Unmarshal(bytes.TrimSpace(line), &frame); err != nil {
				return exitCode, fmt.Errorf("decode exec frame: %w", err)
			}
			switch {
			case frame.Exit != nil:
				exitCode = frame.Exit.Code
				exitErr = frame.Exit.Error
				sawExit = true
			case frame.Stream == "stdout" || frame.Stream == "stderr":
				decoded, derr := base64.StdEncoding.DecodeString(frame.Data)
				if derr != nil {
					return exitCode, fmt.Errorf("decode %s base64: %w", frame.Stream, derr)
				}
				w := stdout
				if frame.Stream == "stderr" {
					w = stderr
				}
				if w != nil {
					if _, werr := w.Write(decoded); werr != nil {
						return exitCode, fmt.Errorf("write %s: %w", frame.Stream, werr)
					}
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return exitCode, fmt.Errorf("read exec stream: %w", readErr)
		}
	}

	if !sawExit {
		return exitCode, errors.New("agent exec stream ended without an exit frame")
	}
	if exitErr != "" {
		return exitCode, fmt.Errorf("agent exec error: %s", exitErr)
	}
	return exitCode, nil
}

// UploadFile writes the bytes read from r to path inside the instance via the
// agent. mode is the desired file mode; pass 0 to let the agent choose a
// default. Returns nil on success (the bytesWritten count is discarded).
func (c *Client) UploadFile(
	ctx context.Context,
	instanceID, path string,
	mode os.FileMode,
	r io.Reader,
) error {
	q := url.Values{}
	q.Set("path", path)
	if mode != 0 {
		q.Set("mode", fmt.Sprintf("%04o", mode.Perm()))
	}
	u := c.endpoint + "/v1/xcloud/instances/" + url.PathEscape(instanceID) +
		"/agent/files?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, r)
	if err != nil {
		return fmt.Errorf("build upload request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Accept", "application/json")

	resp, err := c.streamHC.Do(req)
	if err != nil {
		return fmt.Errorf("POST agent/files: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parseAPIErrorResponse(resp)
	}
	// Drain so the connection can be reused.
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// DownloadFile streams the bytes of path inside the instance to w. The API
// emulates the read via `cat` over the agent exec stream and caps the size;
// an oversized file surfaces as an *APIError with status 413.
func (c *Client) DownloadFile(
	ctx context.Context,
	instanceID, path string,
	w io.Writer,
) error {
	q := url.Values{}
	q.Set("path", path)
	u := c.endpoint + "/v1/xcloud/instances/" + url.PathEscape(instanceID) +
		"/agent/files?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("build download request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/octet-stream")

	resp, err := c.streamHC.Do(req)
	if err != nil {
		return fmt.Errorf("GET agent/files: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parseAPIErrorResponse(resp)
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("download body: %w", err)
	}
	return nil
}
