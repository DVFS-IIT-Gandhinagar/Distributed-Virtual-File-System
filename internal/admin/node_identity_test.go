package admin

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mspb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/metaserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/metaserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/storage"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/storage/memory"
	"google.golang.org/grpc"
)

// The fileserver's -id is its identity in the cluster. A direct-binary restart
// must relaunch it under the id it registered with, not the console's own
// numeric key, or the node comes back as a different fileserver.
func TestGetPresets_RestartsFileserverUnderItsStableID(t *testing.T) {
	srv := NewAdminServer(nil, "")
	srv.resolver = nil
	srv.nodes["0"] = &NodeState{FsID: "0", NodeID: "fs1", Address: "10.0.171.41:50052"}
	srv.nodes["1"] = &NodeState{FsID: "1", NodeID: "addr:10.0.171.42:50052", Address: "10.0.171.42:50052"} // pre-fs_id build
	o := NewOrchestrator(srv, nil, nil, "ubuntu", "~/.ssh/id_ed25519", "/home/ubuntu/repo")

	presets := o.GetPresets()
	if got := presets["0"].FsID; got != "fs1" {
		t.Fatalf("preset must carry the stable id, got %q", got)
	}
	if got := presets["1"].FsID; got != "1" {
		t.Fatalf("a legacy address-keyed node has no usable id; expected the numeric fallback, got %q", got)
	}

	cmd := o.FormatCommand(&ActionRequest{ActionType: ActionRestart, RestartMode: "binary"}, "0", presets["0"])
	if !strings.Contains(cmd, "-id='fs1'") {
		t.Errorf("restart must relaunch under -id=fs1: %s", cmd)
	}
	if !strings.Contains(cmd, "pkill -f 'fileserver -id=fs1'") {
		t.Errorf("the kill pattern must match the running process: %s", cmd)
	}
	if strings.Contains(cmd, "-id='0'") || strings.Contains(cmd, "-id=0") {
		t.Errorf("the console's numeric key leaked into the fileserver identity: %s", cmd)
	}

	// The id comes from a remote party, so it is shell-quoted: the payload may
	// only ever appear inside a single-quoted word.
	hostile := &NodeRestartParams{FsID: "fs1'; touch /tmp/pwned; echo '", Address: "10.0.0.1:50052", Host: "10.0.0.1", Port: 50052}
	cmd = o.FormatCommand(&ActionRequest{ActionType: ActionRestart, RestartMode: "binary"}, "0", hostile)
	if !strings.Contains(cmd, "-id="+shellQuote(hostile.FsID)) {
		t.Errorf("fs_id must be shell-quoted: %s", cmd)
	}
	if strings.Contains(cmd, "-id=fs1';") || strings.Contains(cmd, "-id='fs1';") {
		t.Errorf("fs_id was interpolated unquoted into a shell command: %s", cmd)
	}
}

// A direct-binary restart of the metaserver or admin runs on the target host,
// which has its own MongoDB configuration. Pushing the console's URI there
// pointed a metaserver on another host at that host's loopback (an empty or
// absent database) and put the console's credentials on a remote command line.
func TestFormatCommandBinaryRestartUsesTargetHostMongoConfig(t *testing.T) {
	t.Setenv("MONGO_URI", "mongodb://console-host:27017/dvfs")
	o := &Orchestrator{defaultRepoPath: "/home/ubuntu/repo"}
	params := &NodeRestartParams{FsID: "fs1", Address: "10.7.52.85:50052", Host: "10.7.52.85", Port: 50052}

	for _, service := range []string{"metaserver", "admin", "all"} {
		t.Run(service, func(t *testing.T) {
			cmd := o.FormatCommand(&ActionRequest{ActionType: ActionRestart, RestartMode: "binary", TargetService: service}, "0", params)
			if strings.Contains(cmd, "-mongo_uri") || strings.Contains(cmd, "-mongo_db") || strings.Contains(cmd, "console-host") {
				t.Errorf("%s restart must not carry the console's Mongo target: %s", service, cmd)
			}
			if strings.Contains(cmd, "-state_file") {
				t.Errorf("%s restart still passes the removed -state_file flag: %s", service, cmd)
			}
			if !strings.Contains(cmd, `[ -n "$MONGO_URI" ]`) {
				t.Errorf("%s restart must refuse to launch a binary that would die on a missing MONGO_URI: %s", service, cmd)
			}
			if !strings.Contains(cmd, ". ~/.profile") {
				t.Errorf("%s restart must load the target's login environment: %s", service, cmd)
			}
		})
	}
}

// Hiding a node the cluster still routes to is worse than an error: the
// operator believes it is gone while users keep landing on it. A failed
// deregistration leaves the node visible and reports the failure.
func TestHandleRemoveNode_DeregisterFailureLeavesNodeVisible(t *testing.T) {
	// A port nothing listens on, so the RPC fails fast.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := lis.Addr().String()
	lis.Close()

	srv := newStoreBackedAdmin(memory.New())
	srv.authManager = nil
	srv.SetMetaServerAddr(deadAddr)
	srv.nodes["0"] = &NodeState{FsID: "0", NodeID: "fs1", Address: "10.0.171.41:50052"}
	srv.users["alice"] = "0"

	req := httptest.NewRequest(http.MethodDelete, "/api/nodes/0", nil)
	rec := httptest.NewRecorder()
	srv.handleRemoveNode(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 when the metaserver cannot be reached, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["success"] != false || resp["error"] == nil {
		t.Fatalf("response must say the removal did not happen: %v", resp)
	}
	if _, still := srv.nodes["0"]; !still {
		t.Errorf("node was hidden although the cluster was not changed")
	}
	if _, tomb := srv.removedNodes["0"]; tomb {
		t.Errorf("a tombstone was written although the cluster was not changed")
	}
	if srv.users["alice"] != "0" {
		t.Errorf("alice's mapping was dropped although the cluster was not changed")
	}
}

// Users orphaned on a node that has no record cannot be released through the
// node list, because the node is not in it. DELETE on the stable id reaches
// the metaserver anyway, which releases everyone homed on that id.
func TestHandleRemoveNode_ReleasesOrphansByStableID(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	if _, err := store.UpsertFileServer(ctx, storage.FileServerRecord{NodeID: "fs-keep", Address: "10.0.172.42:50052", Status: "healthy"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AssignUsers(ctx, []storage.UserRecord{
		{Username: "otheruser", HomeNodeID: "fs-keep"},
		{Username: "ghostuser", HomeNodeID: "fs-ghost"}, // no fs-ghost record: orphaned at boot
	}); err != nil {
		t.Fatal(err)
	}

	ms, err := metaserver.NewMetaServer(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	grpcServer := grpc.NewServer()
	mspb.RegisterMetaServerServer(grpcServer, metaserver.NewGRPCHandler(ms))
	go grpcServer.Serve(lis)
	defer grpcServer.Stop()

	// ghostuser is refused until the node returns or an operator releases them.
	roots, err := metaserver.NewGRPCHandler(ms).GetRoots(ctx, &mspb.GetRootsRequest{Username: "ghostuser"})
	if err != nil || roots.Success {
		t.Fatalf("ghostuser should be refused while orphaned: err=%v resp=%v", err, roots)
	}

	srv := newStoreBackedAdmin(store)
	srv.authManager = nil
	srv.SetMetaServerAddr(lis.Addr().String())
	srv.refreshNodes()
	if _, listed := srv.users["ghostuser"]; listed {
		t.Fatalf("an orphan has no node to be listed under")
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/nodes/fs-ghost", nil)
	rec := httptest.NewRecorder()
	srv.handleRemoveNode(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	snap, err := store.LoadSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range snap.Users {
		if u.Username == "ghostuser" {
			t.Fatalf("ghostuser still homed on %s", u.HomeNodeID)
		}
	}
	if len(snap.Users) != 1 || len(snap.FileServers) != 1 {
		t.Fatalf("release touched more than the orphan: %+v / %+v", snap.Users, snap.FileServers)
	}

	// Released users are placed afresh on their next login.
	roots, err = metaserver.NewGRPCHandler(ms).GetRoots(ctx, &mspb.GetRootsRequest{Username: "ghostuser"})
	if err != nil || !roots.Success {
		t.Fatalf("ghostuser should get a fresh home after release: err=%v resp=%v", err, roots)
	}

	// An unknown id with no metaserver configured is still a plain 404.
	bare := newStoreBackedAdmin(memory.New())
	bare.authManager = nil
	rec = httptest.NewRecorder()
	bare.handleRemoveNode(rec, httptest.NewRequest(http.MethodDelete, "/api/nodes/fs-nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 without a metaserver, got %d", rec.Code)
	}
}
