package fileserver

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
)

// StartMetricsHTTP starts a lightweight HTTP server on the given port that
// exposes /metrics (JSON) and /health endpoints.
func (fs *FileServer) StartMetricsHTTP(port int) {
	mux := http.NewServeMux()

	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		m := fs.CollectMetrics()
		if err := json.NewEncoder(w).Encode(m); err != nil {
			log.Printf("[FILESERVER] metrics encode error: %v", err)
		}
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		fmt.Fprintf(w, `{"status":"ok"}`)
	})

	go func() {
		addr := fmt.Sprintf("0.0.0.0:%d", port)
		log.Printf("[FILESERVER] Metrics HTTP on :%d", port)
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Printf("[FILESERVER] Metrics HTTP error: %v", err)
		}
	}()
}
