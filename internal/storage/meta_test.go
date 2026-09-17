package storage_test

import (
	"testing"

	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/storage"
)

func TestNormalizeSharePath(t *testing.T) {
	cases := map[string]string{
		"alice/proj":   "alice/proj",
		"/alice/proj":  "alice/proj",
		"alice/proj/":  "alice/proj",
		"/alice/proj/": "alice/proj",
		`alice\proj`:   "alice/proj",
		"alice//proj":  "alice/proj",
		"./alice/proj": "alice/proj",
		"alice/./proj": "alice/proj",
		"":             "",
		"/":            "",
		"alice":        "alice",
	}
	for in, want := range cases {
		if got := storage.NormalizeSharePath(in); got != want {
			t.Errorf("NormalizeSharePath(%q) = %q, want %q", in, got, want)
		}
	}
}
