package mongo_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/storage"
	mongostore "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/storage/mongo"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/storage/storagetest"
)

// TestMongoStoreConformance runs the shared backend contract against a real
// MongoDB. It is skipped unless DVFS_MONGO_TEST_URI is set, so the default
// `go test ./...` stays hermetic; CI sets it against a service container.
func TestMongoStoreConformance(t *testing.T) {
	uri := os.Getenv("DVFS_MONGO_TEST_URI")
	if uri == "" {
		t.Skip("DVFS_MONGO_TEST_URI not set; skipping MongoDB backend conformance")
	}

	var dbCounter int
	storagetest.RunMetaStoreConformance(t, func(t *testing.T) storage.MetaStore {
		dbCounter++
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		// A dedicated database per subtest keeps them independent even when the
		// suite runs against a shared server.
		s, err := mongostore.Open(ctx, mongostore.Config{
			URI:      uri,
			Database: dbNameFor(t, dbCounter),
			AppName:  "dvfs-conformance",
		})
		if err != nil {
			t.Fatalf("open mongo store: %v", err)
		}
		if err := s.Drop(ctx); err != nil {
			t.Fatalf("drop stale test data: %v", err)
		}
		if err := s.EnsureIndexes(ctx); err != nil {
			t.Fatalf("ensure indexes: %v", err)
		}

		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cleanupCancel()
			_ = s.Drop(cleanupCtx)
			_ = s.Close(cleanupCtx)
		})
		return s
	})
}

// testRunID makes database names unique per process, so two test runs
// sharing one server (a CI matrix, or a developer and a CI job on the same
// machine) never drop each other's data.
var testRunID = fmt.Sprintf("%d_%d", os.Getpid(), time.Now().UnixNano()%1_000_000)

func dbNameFor(t *testing.T, n int) string {
	t.Helper()
	// Mongo database names are limited in length and character set; a counter
	// keeps them short and legal regardless of the subtest name.
	return fmt.Sprintf("dvfs_test_%s_%d", testRunID, n)
}

// TestDatabaseResolution pins the precedence between -mongo_db and the database
// in the connection URI. Putting the database in the URI path is the MongoDB
// convention, so silently ignoring it would send every write to the wrong
// database with no error anywhere.
func TestDatabaseResolution(t *testing.T) {
	uri := os.Getenv("DVFS_MONGO_TEST_URI")
	if uri == "" {
		t.Skip("DVFS_MONGO_TEST_URI not set; skipping MongoDB database resolution test")
	}

	cases := []struct {
		name     string
		uriPath  string
		override string
		want     string
	}{
		{"URIDatabaseIsHonoured", "/dvfs_from_uri", "", "dvfs_from_uri"},
		{"NoDatabaseAnywhereFallsBackToDefault", "", "", mongostore.DefaultDatabase},
		{"ExplicitOverrideWins", "/dvfs_from_uri", "dvfs_override", "dvfs_override"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			s, err := mongostore.Open(ctx, mongostore.Config{
				URI:      trimURIPath(uri) + tc.uriPath,
				Database: tc.override,
				AppName:  "dvfs-db-resolution",
			})
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			// Nothing is written here, so nothing is dropped: one of these cases
			// resolves to the default "dvfs" database, which on a developer's
			// machine is the real one.
			t.Cleanup(func() {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cleanupCancel()
				_ = s.Close(cleanupCtx)
			})

			if got := s.DatabaseName(); got != tc.want {
				t.Fatalf("database resolved to %q, want %q", got, tc.want)
			}
		})
	}
}

// trimURIPath strips any existing database path so the cases can append their own.
func trimURIPath(uri string) string {
	const scheme = "://"
	i := strings.Index(uri, scheme)
	if i < 0 {
		return uri
	}
	rest := uri[i+len(scheme):]
	if j := strings.IndexAny(rest, "/?"); j >= 0 {
		return uri[:i+len(scheme)] + rest[:j]
	}
	return uri
}
