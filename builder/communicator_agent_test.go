package builder

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/hashicorp/packer-plugin-sdk/packer"

	"github.com/studio-ch/packer-plugin-xcloud/apiclient"
)

func frame(stream, data string) string {
	return fmt.Sprintf(`{"stream":%q,"data":%q}`+"\n", stream,
		base64.StdEncoding.EncodeToString([]byte(data)))
}

// TestAgentCommunicatorStart proves Start streams stdout and records the exit
// code via the RemoteCmd, running the command through `/bin/sh -c`.
func TestAgentCommunicatorStart(t *testing.T) {
	var gotArgv []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Argv []string `json:"argv"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		gotArgv = body.Argv
		w.Header().Set("Content-Type", "application/x-ndjson")
		io.WriteString(w, frame("stdout", "root\n"))
		io.WriteString(w, `{"exit":{"code":0,"timedOut":false,"error":""}}`+"\n")
	}))
	defer srv.Close()

	client := apiclient.New(srv.URL, "tok", nil)
	comm := newAgentCommunicator(context.Background(), client, "inst-1")

	var stdout bytes.Buffer
	cmd := &packer.RemoteCmd{Command: "whoami", Stdout: &stdout}
	if err := comm.Start(context.Background(), cmd); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if code := cmd.Wait(); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if stdout.String() != "root\n" {
		t.Errorf("stdout = %q, want root\\n", stdout.String())
	}
	if len(gotArgv) != 3 || gotArgv[0] != "/bin/sh" || gotArgv[1] != "-c" || gotArgv[2] != "whoami" {
		t.Errorf("argv = %v, want [/bin/sh -c whoami]", gotArgv)
	}
}

func TestAgentCommunicatorStartNonZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		io.WriteString(w, `{"exit":{"code":3,"timedOut":false,"error":""}}`+"\n")
	}))
	defer srv.Close()

	client := apiclient.New(srv.URL, "tok", nil)
	comm := newAgentCommunicator(context.Background(), client, "inst-1")
	cmd := &packer.RemoteCmd{Command: "exit 3"}
	if err := comm.Start(context.Background(), cmd); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if code := cmd.Wait(); code != 3 {
		t.Fatalf("exit code = %d, want 3", code)
	}
}

// TestAgentCommunicatorStartTransportError proves a pre-stream API failure
// yields a non-zero exit (not a false success).
func TestAgentCommunicatorStartTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, `{"status":503,"title":"Bad Gateway","detail":"upstream down"}`)
	}))
	defer srv.Close()

	client := apiclient.New(srv.URL, "tok", nil)
	comm := newAgentCommunicator(context.Background(), client, "inst-1")
	var stderr bytes.Buffer
	cmd := &packer.RemoteCmd{Command: "whoami", Stderr: &stderr}
	if err := comm.Start(context.Background(), cmd); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if code := cmd.Wait(); code == 0 {
		t.Fatalf("exit code = 0, want non-zero on transport error")
	}
}

func TestAgentCommunicatorUpload(t *testing.T) {
	var gotBody []byte
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Query().Get("path")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"bytesWritten":%d}`, len(gotBody))
	}))
	defer srv.Close()

	client := apiclient.New(srv.URL, "tok", nil)
	comm := newAgentCommunicator(context.Background(), client, "inst-1")
	payload := []byte("hello world")
	if err := comm.Upload("/tmp/x", bytes.NewReader(payload), nil); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if gotPath != "/tmp/x" {
		t.Errorf("path = %q", gotPath)
	}
	if !bytes.Equal(gotBody, payload) {
		t.Errorf("body = %q, want %q", gotBody, payload)
	}
}

func TestAgentCommunicatorDownloadDirUnsupported(t *testing.T) {
	comm := newAgentCommunicator(context.Background(), apiclient.New("x", "t", nil), "inst-1")
	if err := comm.DownloadDir("/a", "/b", nil); err == nil {
		t.Fatal("expected DownloadDir to be unsupported")
	}
}

// TestAgentCommunicatorUploadDir walks a temp tree and uploads each file with
// the basename-dir recreated under dst (no trailing slash on src).
func TestAgentCommunicatorUploadDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/a.txt", []byte("A"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir+"/sub", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/sub/b.txt", []byte("B"), 0o644); err != nil {
		t.Fatal(err)
	}

	gotPaths := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotPaths[r.URL.Query().Get("path")] = string(body)
		fmt.Fprintf(w, `{"bytesWritten":%d}`, len(body))
	}))
	defer srv.Close()

	client := apiclient.New(srv.URL, "tok", nil)
	comm := newAgentCommunicator(context.Background(), client, "inst-1")

	srcBase := lastPathSegment(dir)
	if err := comm.UploadDir("/remote", dir, nil); err != nil {
		t.Fatalf("UploadDir: %v", err)
	}
	wantA := "/remote/" + srcBase + "/a.txt"
	wantB := "/remote/" + srcBase + "/sub/b.txt"
	if gotPaths[wantA] != "A" {
		t.Errorf("missing/incorrect %s: %q (all=%v)", wantA, gotPaths[wantA], gotPaths)
	}
	if gotPaths[wantB] != "B" {
		t.Errorf("missing/incorrect %s: %q (all=%v)", wantB, gotPaths[wantB], gotPaths)
	}
}

func lastPathSegment(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}
