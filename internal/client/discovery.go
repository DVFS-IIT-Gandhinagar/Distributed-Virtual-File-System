package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultGistURL points to the canonical machines.json published by the cluster.
	DefaultGistURL = "https://gist.githubusercontent.com/dvfs-iitgn/6eb8da397735b83f76b54af4cca64c83/raw/machines.json"
	// DefaultGistTimeout is the maximum duration to wait for a Gist response.
	DefaultGistTimeout = 8 * time.Second
	// DefaultCacheTTL is the duration after which cached entries are considered stale.
	DefaultCacheTTL = 5 * time.Minute
)

// GistNode represents a node entry in the cluster's machines.json.
type GistNode struct {
	Username string `json:"username"` // e.g. "dvfs1", "dvfs2"
	MAC      string `json:"mac"`      // e.g. "28:cd:c1:0e:25:70"
	IP       string `json:"ip"`       // e.g. "10.0.171.38"
	LastSeen string `json:"last_seen"`
}

// DiscoveryOption configures a DiscoveryResolver.
type DiscoveryOption func(*DiscoveryResolver)

// WithGistURL overrides the default Gist URL.
func WithGistURL(url string) DiscoveryOption {
	return func(d *DiscoveryResolver) {
		if url != "" {
			d.gistURL = url
		}
	}
}

// WithHTTPClient overrides the default HTTP client.
func WithHTTPClient(client *http.Client) DiscoveryOption {
	return func(d *DiscoveryResolver) {
		if client != nil {
			d.httpClient = client
		}
	}
}

// WithCacheFile overrides the local disk cache path.
func WithCacheFile(path string) DiscoveryOption {
	return func(d *DiscoveryResolver) {
		d.cacheFile = path
	}
}

// WithCacheTTL sets the time-to-live for cached discovery entries.
func WithCacheTTL(ttl time.Duration) DiscoveryOption {
	return func(d *DiscoveryResolver) {
		if ttl > 0 {
			d.cacheTTL = ttl
		}
	}
}

// DiscoveryResolver dynamically resolves cluster node hostnames to IPs and vice versa.
type DiscoveryResolver struct {
	gistURL    string
	httpClient *http.Client
	cacheFile  string
	cacheTTL   time.Duration

	mu        sync.RWMutex
	hostToIP  map[string]string // "dvfs1" -> "10.0.171.38"
	ipToHost  map[string]string // "10.0.171.38" -> "dvfs1"
	lastFetch time.Time
}

// defaultCachePath returns ~/.dvfs/machines_cache.json or a temp directory fallback.
func defaultCachePath() string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		return filepath.Join(home, ".dvfs", "machines_cache.json")
	}
	return filepath.Join(os.TempDir(), "dvfs_machines_cache.json")
}

// NewDiscoveryResolver initializes a resolver with default or custom options.
func NewDiscoveryResolver(opts ...DiscoveryOption) *DiscoveryResolver {
	d := &DiscoveryResolver{
		gistURL: DefaultGistURL,
		httpClient: &http.Client{
			Timeout: DefaultGistTimeout,
		},
		cacheFile: defaultCachePath(),
		cacheTTL:  DefaultCacheTTL,
		hostToIP:  make(map[string]string),
		ipToHost:  make(map[string]string),
	}

	for _, opt := range opts {
		opt(d)
	}

	// Try loading from local disk cache on startup for immediate availability
	_ = d.loadFromDiskCache()

	return d
}

// FetchNodes polls the GitHub Gist over HTTPS, updates memory caches, and persists to disk.
// If the network request fails, it falls back to the persisted disk cache.
func (d *DiscoveryResolver) FetchNodes(ctx context.Context) (map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.gistURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build gist request: %w", err)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		// Network failed: attempt disk cache fallback
		if loadErr := d.loadFromDiskCache(); loadErr == nil {
			d.mu.RLock()
			defer d.mu.RUnlock()
			if len(d.hostToIP) > 0 {
				cp := make(map[string]string, len(d.hostToIP))
				for k, v := range d.hostToIP {
					cp[k] = v
				}
				return cp, nil
			}
		}
		return nil, fmt.Errorf("gist fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Non-200 response: attempt disk cache fallback
		if loadErr := d.loadFromDiskCache(); loadErr == nil {
			d.mu.RLock()
			defer d.mu.RUnlock()
			if len(d.hostToIP) > 0 {
				cp := make(map[string]string, len(d.hostToIP))
				for k, v := range d.hostToIP {
					cp[k] = v
				}
				return cp, nil
			}
		}
		return nil, fmt.Errorf("gist returned non-200 HTTP status: %d", resp.StatusCode)
	}

	var nodes []GistNode
	if err := json.NewDecoder(resp.Body).Decode(&nodes); err != nil {
		return nil, fmt.Errorf("failed to decode gist JSON: %w", err)
	}

	d.applyNodes(nodes)
	_ = d.saveToDiskCache(nodes)

	d.mu.RLock()
	defer d.mu.RUnlock()
	res := make(map[string]string, len(d.hostToIP))
	for k, v := range d.hostToIP {
		res[k] = v
	}
	return res, nil
}

// applyNodes updates in-memory mapping tables.
func (d *DiscoveryResolver) applyNodes(nodes []GistNode) {
	d.mu.Lock()
	defer d.mu.Unlock()

	freshHostToIP := make(map[string]string)
	freshIPToHost := make(map[string]string)

	for _, n := range nodes {
		u := strings.TrimSpace(n.Username)
		ip := strings.TrimSpace(n.IP)
		if u != "" && ip != "" {
			freshHostToIP[u] = ip
			freshIPToHost[ip] = u
		}
	}

	d.hostToIP = freshHostToIP
	d.ipToHost = freshIPToHost
	d.lastFetch = time.Now()
}

// Lookup returns the IP address for the given node identifier (e.g. "dvfs1").
// If the cache is empty or stale, it attempts a live fetch.
func (d *DiscoveryResolver) Lookup(nodeName string) (string, error) {
	d.mu.RLock()
	stale := d.lastFetch.IsZero() || time.Since(d.lastFetch) > d.cacheTTL
	ip, ok := d.hostToIP[nodeName]
	d.mu.RUnlock()

	if ok && !stale {
		return ip, nil
	}

	// Fetch fresh nodes
	fresh, err := d.FetchNodes(context.Background())
	if err != nil {
		// If fetch failed but we had a stale entry, return it as fallback
		if ok {
			return ip, nil
		}
		return "", fmt.Errorf("lookup failed for node %s: %w", nodeName, err)
	}

	if freshIP, found := fresh[nodeName]; found {
		return freshIP, nil
	}

	return "", fmt.Errorf("node %s not found in discovery list", nodeName)
}

// ResolveServerName determines the TLS SNI / SAN name to verify against.
// Given "10.0.171.38:50051", it extracts "10.0.171.38", checks if it maps to "dvfs1",
// and returns "dvfs1". For "127.0.0.1" or "localhost", it returns "localhost".
func (d *DiscoveryResolver) ResolveServerName(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	host = strings.TrimSpace(host)

	// If already a hostname (not an IP address), return as is
	parsedIP := net.ParseIP(host)
	if parsedIP == nil {
		return host
	}

	// Loopback IP
	if parsedIP.IsLoopback() {
		return "localhost"
	}

	// Check reverse lookup
	d.mu.RLock()
	mappedHost, ok := d.ipToHost[host]
	stale := d.lastFetch.IsZero() || time.Since(d.lastFetch) > d.cacheTTL
	d.mu.RUnlock()

	if ok && mappedHost != "" && !stale {
		return mappedHost
	}

	// Try fetching fresh nodes if not cached or stale
	fetchCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, fetchErr := d.FetchNodes(fetchCtx); fetchErr == nil {
		d.mu.RLock()
		mappedHost, ok = d.ipToHost[host]
		d.mu.RUnlock()
		if ok && mappedHost != "" {
			return mappedHost
		}
	} else if ok && mappedHost != "" {
		return mappedHost
	}

	// Fallback to searching current cache
	return host
}

// ResolveMetaAddress resolves the metaserver (dvfs1) IP and formats host:port.
func (d *DiscoveryResolver) ResolveMetaAddress(defaultPort string) (string, error) {
	ip, err := d.Lookup("dvfs1")
	if err != nil {
		return "", err
	}
	if defaultPort == "" {
		defaultPort = "50051"
	}
	return fmt.Sprintf("%s:%s", ip, defaultPort), nil
}

// SetNodes manually sets node mappings (primarily for unit tests and local overrides).
func (d *DiscoveryResolver) SetNodes(nodes []GistNode) {
	d.applyNodes(nodes)
}

// saveToDiskCache persists node list to local cache file.
func (d *DiscoveryResolver) saveToDiskCache(nodes []GistNode) error {
	if d.cacheFile == "" {
		return nil
	}

	dir := filepath.Dir(d.cacheFile)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(nodes, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(d.cacheFile, data, 0600)
}

// loadFromDiskCache reads node list from local cache file.
func (d *DiscoveryResolver) loadFromDiskCache() error {
	if d.cacheFile == "" {
		return os.ErrNotExist
	}

	data, err := os.ReadFile(d.cacheFile)
	if err != nil {
		return err
	}

	var nodes []GistNode
	if err := json.NewDecoder(strings.NewReader(string(data))).Decode(&nodes); err != nil {
		return err
	}

	d.applyNodes(nodes)
	return nil
}
