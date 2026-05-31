package apiclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const imagesListBody = `{"data":[
  {"name":"macos-tahoe","regionId":"4a3a130c-ff4d-4a00-abf9-f075a2008a8d","regionSlug":"BIT1"},
  {"name":"macos-sequoia","regionId":"4a3a130c-ff4d-4a00-abf9-f075a2008a8d","regionSlug":"BIT1"},
  {"name":"ubuntu","regionId":"99999999-9999-9999-9999-999999999999","regionSlug":"ZRH2"}
]}`

func newImagesServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/xcloud/images" {
			t.Errorf("path = %q, want /v1/xcloud/images", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("auth header = %q, want \"Bearer tok\"", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(imagesListBody))
	}))
}

func TestResolveRegionIDExactMatch(t *testing.T) {
	srv := newImagesServer(t)
	defer srv.Close()
	c := New(srv.URL, "tok", srv.Client())

	id, err := c.ResolveRegionID(context.Background(), "BIT1")
	if err != nil {
		t.Fatalf("ResolveRegionID: %v", err)
	}
	if id != "4a3a130c-ff4d-4a00-abf9-f075a2008a8d" {
		t.Errorf("id = %q, want BIT1 uuid", id)
	}
}

func TestResolveRegionIDCaseInsensitive(t *testing.T) {
	srv := newImagesServer(t)
	defer srv.Close()
	c := New(srv.URL, "tok", srv.Client())

	for _, slug := range []string{"bit1", "Bit1", "  BIT1  "} {
		id, err := c.ResolveRegionID(context.Background(), slug)
		if err != nil {
			t.Fatalf("ResolveRegionID(%q): %v", slug, err)
		}
		if id != "4a3a130c-ff4d-4a00-abf9-f075a2008a8d" {
			t.Errorf("ResolveRegionID(%q) = %q, want BIT1 uuid", slug, id)
		}
	}
}

func TestResolveRegionIDNotFound(t *testing.T) {
	srv := newImagesServer(t)
	defer srv.Close()
	c := New(srv.URL, "tok", srv.Client())

	_, err := c.ResolveRegionID(context.Background(), "NOPE")
	if err == nil {
		t.Fatal("expected error for unknown region slug, got nil")
	}
	// The error should list the available slugs to help the user.
	if !strings.Contains(err.Error(), "BIT1") || !strings.Contains(err.Error(), "ZRH2") {
		t.Errorf("error %q should list available regions (BIT1, ZRH2)", err.Error())
	}
}
