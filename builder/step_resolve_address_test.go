package builder

import (
	"testing"

	"github.com/studio-ch/packer-plugin-xcloud/apiclient"
)

func strPtr(s string) *string { return &s }

func TestElasticIPUsable(t *testing.T) {
	const addr = "185.220.159.62"

	tests := []struct {
		name     string
		eip      apiclient.ElasticIP
		wantAddr string
		wantOK   bool
	}{
		{
			name:     "ready with address -> accept",
			eip:      apiclient.ElasticIP{ID: "eip-1", PublicAddress: strPtr(addr), Status: "ready"},
			wantAddr: addr,
			wantOK:   true,
		},
		{
			name:     "bound with address -> accept",
			eip:      apiclient.ElasticIP{ID: "eip-2", PublicAddress: strPtr(addr), Status: "bound"},
			wantAddr: addr,
			wantOK:   true,
		},
		{
			name:   "ready without address -> keep waiting",
			eip:    apiclient.ElasticIP{ID: "eip-3", PublicAddress: nil, Status: "ready"},
			wantOK: false,
		},
		{
			name:   "ready with empty address -> keep waiting",
			eip:    apiclient.ElasticIP{ID: "eip-4", PublicAddress: strPtr(""), Status: "ready"},
			wantOK: false,
		},
		{
			name:   "allocating with address -> keep waiting",
			eip:    apiclient.ElasticIP{ID: "eip-5", PublicAddress: strPtr(addr), Status: "allocating"},
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAddr, gotOK := elasticIPUsable(tt.eip)
			if gotOK != tt.wantOK {
				t.Fatalf("elasticIPUsable() ok = %v, want %v", gotOK, tt.wantOK)
			}
			if gotOK && gotAddr != tt.wantAddr {
				t.Errorf("elasticIPUsable() addr = %q, want %q", gotAddr, tt.wantAddr)
			}
			if !gotOK && gotAddr != "" {
				t.Errorf("elasticIPUsable() addr = %q, want empty when not usable", gotAddr)
			}
		})
	}
}
