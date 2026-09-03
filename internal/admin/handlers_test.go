package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHandleCluster(t *testing.T) {
	admin := NewAdminServer("", "")
	admin.users["alice"] = "0"
	admin.users["bob"] = "0"

	admin.nodes["0"] = &NodeState{
		FsID:    "0",
		Address: "127.0.0.1:50052",
		Status:  StatusOnline,
		Metrics: &FileserverMetrics{
			DiskTotalBytes: 500,
			DiskUsedBytes:  100,
		},
		History: NewRingBuffer(10),
	}
	admin.nodes["1"] = &NodeState{
		FsID:    "1",
		Address: "127.0.0.1:50053",
		Status:  StatusOffline,
		Metrics: nil,
		History: NewRingBuffer(10),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/cluster", nil)
	rec := httptest.NewRecorder()

	admin.handleCluster(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var resp ClusterResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode cluster response: %v", err)
	}

	if resp.NodeCount != 2 {
		t.Errorf("expected NodeCount=2, got %d", resp.NodeCount)
	}
	if resp.OnlineCount != 1 {
		t.Errorf("expected OnlineCount=1, got %d", resp.OnlineCount)
	}
	if resp.TotalStorageBytes != 500 {
		t.Errorf("expected TotalStorageBytes=500, got %d", resp.TotalStorageBytes)
	}
	if resp.UsedStorageBytes != 100 {
		t.Errorf("expected UsedStorageBytes=100, got %d", resp.UsedStorageBytes)
	}
	if resp.TotalUsers != 2 {
		t.Errorf("expected TotalUsers=2, got %d", resp.TotalUsers)
	}

	// Method not allowed
	postReq := httptest.NewRequest(http.MethodPost, "/api/cluster", nil)
	postRec := httptest.NewRecorder()
	admin.handleCluster(postRec, postReq)
	if postRec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 on POST, got %d", postRec.Code)
	}
}

func TestHandleHistory(t *testing.T) {
	admin := NewAdminServer("", "")
	rb := NewRingBuffer(10)
	rb.Push(Snapshot{Timestamp: 100})
	rb.Push(Snapshot{Timestamp: 200})

	admin.nodes["0"] = &NodeState{
		FsID:    "0",
		History: rb,
	}

	// Success case
	req := httptest.NewRequest(http.MethodGet, "/api/history/0", nil)
	rec := httptest.NewRecorder()
	admin.handleHistory(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for existing history, got %d", rec.Code)
	}

	var snapshots []Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&snapshots); err != nil {
		t.Fatalf("failed to decode snapshots: %v", err)
	}
	if len(snapshots) != 2 {
		t.Errorf("expected 2 snapshots, got %d", len(snapshots))
	}

	// 404 case
	req404 := httptest.NewRequest(http.MethodGet, "/api/history/999", nil)
	rec404 := httptest.NewRecorder()
	admin.handleHistory(rec404, req404)

	if rec404.Code != http.StatusNotFound {
		t.Errorf("expected 404 for missing history, got %d", rec404.Code)
	}
}

func TestSpaHandler(t *testing.T) {
	tempDir := t.TempDir()
	indexContent := "<html><body>Index</body></html>"
	assetContent := "body { color: red; }"

	_ = os.WriteFile(filepath.Join(tempDir, "index.html"), []byte(indexContent), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "style.css"), []byte(assetContent), 0644)

	spa := spaHandler{
		staticDir: tempDir,
		fs:        http.FileServer(http.Dir(tempDir)),
	}

	// 1. Existing static asset
	reqAsset := httptest.NewRequest(http.MethodGet, "/style.css", nil)
	recAsset := httptest.NewRecorder()
	spa.ServeHTTP(recAsset, reqAsset)
	if recAsset.Code != http.StatusOK || recAsset.Body.String() != assetContent {
		t.Errorf("expected asset content, got code=%d body=%q", recAsset.Code, recAsset.Body.String())
	}

	// 2. Non-existing path -> SPA fallback to index.html
	reqSPA := httptest.NewRequest(http.MethodGet, "/nodes/details", nil)
	recSPA := httptest.NewRecorder()
	spa.ServeHTTP(recSPA, reqSPA)
	if recSPA.Code != http.StatusOK || recSPA.Body.String() != indexContent {
		t.Errorf("expected fallback to index.html, got code=%d body=%q", recSPA.Code, recSPA.Body.String())
	}

	// 3. API route non-existing -> returns 404, not index.html
	reqAPI := httptest.NewRequest(http.MethodGet, "/api/unknown", nil)
	recAPI := httptest.NewRecorder()
	spa.ServeHTTP(recAPI, reqAPI)
	if recAPI.Code != http.StatusNotFound {
		t.Errorf("expected 404 for /api/ routes, got code=%d", recAPI.Code)
	}
}
