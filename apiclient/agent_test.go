package apiclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

const testInstanceID = "2ba65959-c879-4dfe-90fe-2784c4093a11"

// ndjsonFrame builds one stdout/stderr NDJSON line in the wire shape.
func ndjsonFrame(stream, data string) string {
	return fmt.Sprintf(`{"stream":%q,"data":%q}`+"\n", stream,
		base64.StdEncoding.EncodeToString([]byte(data)))
}

func TestExecStreamParsesFramesAndExit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wantPath := "/v1/xcloud/instances/" + testInstanceID + "/agent/exec"
		if r.URL.Path != wantPath {
			t.Errorf("path = %q, want %q", r.URL.Path, wantPath)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("auth header = %q", got)
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		io.WriteString(w, ndjsonFrame("stdout", "root\n"))
		if fl != nil {
			fl.Flush()
		}
		io.WriteString(w, ndjsonFrame("stderr", "warn\n"))
		io.WriteString(w, `{"exit":{"code":0,"timedOut":false,"error":""}}`+"\n")
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", nil)
	var stdout, stderr bytes.Buffer
	code, err := c.ExecStream(context.Background(), testInstanceID,
		[]string{"/bin/sh", "-c", "whoami"}, nil, "", &stdout, &stderr)
	if err != nil {
		t.Fatalf("ExecStream: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if stdout.String() != "root\n" {
		t.Errorf("stdout = %q, want %q", stdout.String(), "root\n")
	}
	if stderr.String() != "warn\n" {
		t.Errorf("stderr = %q, want %q", stderr.String(), "warn\n")
	}
}

func TestExecStreamNonZeroExit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		io.WriteString(w, `{"exit":{"code":7,"timedOut":false,"error":""}}`+"\n")
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", nil)
	code, err := c.ExecStream(context.Background(), testInstanceID,
		[]string{"/bin/false"}, nil, "", io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("ExecStream: %v", err)
	}
	if code != 7 {
		t.Errorf("exit code = %d, want 7", code)
	}
}

func TestExecStreamExitError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		io.WriteString(w, `{"exit":{"code":-1,"timedOut":true,"error":"deadline exceeded"}}`+"\n")
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", nil)
	_, err := c.ExecStream(context.Background(), testInstanceID,
		[]string{"/bin/sleep", "999"}, nil, "", io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("expected exit error carrying message, got %v", err)
	}
}

func TestExecStreamPreStreamAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `{"status":403,"title":"Forbidden","detail":"not enabled","code":"agent_exec_disabled"}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", nil)
	_, err := c.ExecStream(context.Background(), testInstanceID,
		[]string{"/bin/true"}, nil, "", io.Discard, io.Discard)
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T (%v)", err, err)
	}
	if apiErr.Status != 403 {
		t.Errorf("status = %d, want 403", apiErr.Status)
	}
}

func TestExecStreamMissingExitFrame(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		io.WriteString(w, ndjsonFrame("stdout", "partial"))
		// no exit frame
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", nil)
	_, err := c.ExecStream(context.Background(), testInstanceID,
		[]string{"/bin/true"}, nil, "", io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "without an exit frame") {
		t.Fatalf("expected missing-exit-frame error, got %v", err)
	}
}

func TestUploadFilePostsBytes(t *testing.T) {
	var gotBody []byte
	var gotPath, gotMode string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		gotPath = r.URL.Query().Get("path")
		gotMode = r.URL.Query().Get("mode")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"bytesWritten":%d}`, len(gotBody))
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", nil)
	payload := []byte("#!/bin/sh\necho hi\n")
	err := c.UploadFile(context.Background(), testInstanceID, "/tmp/run.sh",
		os.FileMode(0o755), bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if gotPath != "/tmp/run.sh" {
		t.Errorf("path = %q", gotPath)
	}
	if gotMode != "0755" {
		t.Errorf("mode = %q, want 0755", gotMode)
	}
	if !bytes.Equal(gotBody, payload) {
		t.Errorf("body = %q, want %q", gotBody, payload)
	}
}

func TestDownloadFileStreamsBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("path"); got != "/etc/hostname" {
			t.Errorf("path = %q", got)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		io.WriteString(w, "my-host")
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", nil)
	var buf bytes.Buffer
	if err := c.DownloadFile(context.Background(), testInstanceID, "/etc/hostname", &buf); err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	if buf.String() != "my-host" {
		t.Errorf("body = %q, want my-host", buf.String())
	}
}

func TestDownloadFileTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		io.WriteString(w, `{"status":413,"title":"Payload Too Large","detail":"too big"}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", nil)
	err := c.DownloadFile(context.Background(), testInstanceID, "/big", io.Discard)
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T (%v)", err, err)
	}
	if apiErr.Status != 413 {
		t.Errorf("status = %d, want 413", apiErr.Status)
	}
}
