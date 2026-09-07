package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func sampleGistData() []GistNode {
	return []GistNode{
		{Username: "dvfs1", MAC: "28:cd:c1:0e:25:70", IP: "10.0.171.38", LastSeen: "2026-09-06 20:00:00"},
		{Username: "dvfs2", MAC: "28:cd:c1:0e:25:71", IP: "10.0.171.39", LastSeen: "2026-09-06 20:00:00"},
		{Username: "dvfs3", MAC: "28:cd:c1:0e:25:72", IP: "10.0.171.40", LastSeen: "2026-09-06 20:00:00"},
	}
}

func TestDiscoveryResolver_FetchAndLookup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sampleGistData())
	}))
	defer server.Close()

	tempDir := t.TempDir()
	cacheFile := filepath.Join(tempDir, "cache.json")

	resolver := NewDiscoveryResolver(
		WithGistURL(server.URL),
		WithCacheFile(cacheFile),
	)

	nodes, err := resolver.FetchNodes(context.Background())
	if err != nil {
		t.Fatalf("FetchNodes failed: %v", err)
	}

	if len(nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(nodes))
	}
	if nodes["dvfs1"] != "10.0.171.38" {
		t.Errorf("expected dvfs1=10.0.171.38, got %s", nodes["dvfs1"])
	}

	// Test Lookup
	ip, err := resolver.Lookup("dvfs1")
	if err != nil {
		t.Fatalf("Lookup dvfs1 failed: %v", err)
	}
	if ip != "10.0.171.38" {
		t.Errorf("expected 10.0.171.38, got %s", ip)
	}

	ip2, err := resolver.Lookup("dvfs2")
	if err != nil {
		t.Fatalf("Lookup dvfs2 failed: %v", err)
	}
	if ip2 != "10.0.171.39" {
		t.Errorf("expected 10.0.171.39, got %s", ip2)
	}

	// Verify disk cache was written
	if _, err := os.Stat(cacheFile); os.IsNotExist(err) {
		t.Errorf("expected disk cache file to exist at %s", cacheFile)
	}
}

func TestDiscoveryResolver_ResolveServerName(t *testing.T) {
	tempDir := t.TempDir()
	cacheFile := filepath.Join(tempDir, "cache.json")

	resolver := NewDiscoveryResolver(WithCacheFile(cacheFile))
	resolver.SetNodes(sampleGistData())

	tests := []struct {
		address  string
		expected string
	}{
		{"10.0.171.38:50051", "dvfs1"},
		{"10.0.171.38", "dvfs1"},
		{"10.0.171.39:50052", "dvfs2"},
		{"10.0.171.40:50052", "dvfs3"},
		{"127.0.0.1:50051", "localhost"},
		{"127.0.0.1", "localhost"},
		{"localhost:50051", "localhost"},
		{"dvfs1:50051", "dvfs1"},
		{"dvfs4:50052", "dvfs4"},
	}

	for _, tc := range tests {
		got := resolver.ResolveServerName(tc.address)
		if got != tc.expected {
			t.Errorf("ResolveServerName(%q) = %q; want %q", tc.address, got, tc.expected)
		}
	}
}

func TestDiscoveryResolver_DiskCacheFallback(t *testing.T) {
	tempDir := t.TempDir()
	cacheFile := filepath.Join(tempDir, "cache.json")

	// Pre-seed disk cache
	data, err := json.Marshal(sampleGistData())
	if err != nil {
		t.Fatalf("failed to marshal sample: %v", err)
	}
	if err := os.WriteFile(cacheFile, data, 0600); err != nil {
		t.Fatalf("failed to write disk cache: %v", err)
	}

	// Create an unreachable HTTP server URL
	resolver := NewDiscoveryResolver(
		WithGistURL("http://127.0.0.1:59999/unreachable"),
		WithCacheFile(cacheFile),
		WithHTTPClient(&http.Client{Timeout: 100 * time.Millisecond}),
	)

	// FetchNodes should fail network request but recover from disk cache
	nodes, err := resolver.FetchNodes(context.Background())
	if err != nil {
		t.Fatalf("FetchNodes failed unexpectedly on fallback: %v", err)
	}

	if nodes["dvfs1"] != "10.0.171.38" {
		t.Errorf("expected dvfs1=10.0.171.38 from cache, got %s", nodes["dvfs1"])
	}
}

func TestDiscoveryResolver_ResolveMetaAddress(t *testing.T) {
	tempDir := t.TempDir()
	cacheFile := filepath.Join(tempDir, "cache.json")

	resolver := NewDiscoveryResolver(WithCacheFile(cacheFile))
	resolver.SetNodes(sampleGistData())

	metaAddr, err := resolver.ResolveMetaAddress("50051")
	if err != nil {
		t.Fatalf("ResolveMetaAddress failed: %v", err)
	}

	expected := "10.0.171.38:50051"
	if metaAddr != expected {
		t.Errorf("ResolveMetaAddress = %q; want %q", metaAddr, expected)
	}
}
