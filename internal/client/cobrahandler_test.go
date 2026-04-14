package client

import (
	"testing"

	"github.com/umangshikarvar/dvfs/internal/domain"
)

func testCobraCacheHandler() *CacheHandler {
	rootFID := &domain.FID{FileServerID: "fs", InodeID: 1, GenerationNumber: 1}
	root := &CNode{
		Name:     "mydrive",
		Type:     domain.InodeTypeDirectory,
		fid:      rootFID,
		children: map[string]*CNode{},
	}

	docs := &CNode{
		Name:     "docs",
		Type:     domain.InodeTypeDirectory,
		fid:      &domain.FID{FileServerID: "fs", InodeID: 2, GenerationNumber: 1},
		children: map[string]*CNode{},
		parent:   root,
	}
	file := &CNode{
		Name:   "notes.txt",
		Type:   domain.InodeTypeFile,
		fid:    &domain.FID{FileServerID: "fs", InodeID: 3, GenerationNumber: 1},
		parent: root,
	}
	root.children[docs.Name] = docs
	root.children[file.Name] = file

	return &CacheHandler{
		root:   root,
		curr:   root,
		client: &Client{},
	}
}

func TestNewCobraHandlerRegistersCoreCommands(t *testing.T) {
	h := NewCobraHandler(testCobraCacheHandler())

	commands := map[string]bool{}
	for _, cmd := range h.rootCmd.Commands() {
		commands[cmd.Name()] = true
	}

	required := []string{
		"ls", "cd", "pwd", "upload", "download", "create", "mkdir", "read",
		"rm", "trash", "restore", "show_trash", "clear_trash", "info", "sharewith", "unsharewith", "viscache", "clear", "exit",
	}

	for _, cmd := range required {
		if !commands[cmd] {
			t.Fatalf("expected command %q to be registered", cmd)
		}
	}
}

func TestCobraCompleterCommandSuggestions(t *testing.T) {
	h := NewCobraHandler(testCobraCacheHandler())
	completer := &CobraCompleter{handler: h}

	suggestions, prefixLen := completer.Do([]rune("d"), 1)
	if prefixLen != 1 {
		t.Fatalf("unexpected prefix length: got=%d want=1", prefixLen)
	}

	foundDownload := false
	for _, s := range suggestions {
		if string(s) == "ownload" {
			foundDownload = true
			break
		}
	}
	if !foundDownload {
		t.Fatalf("expected completion suggestions to include download suffix")
	}
}

func TestCobraCompleterFileArgumentSuggestions(t *testing.T) {
	h := NewCobraHandler(testCobraCacheHandler())
	completer := &CobraCompleter{handler: h}

	suggestions, prefixLen := completer.Do([]rune("cd d"), len([]rune("cd d")))
	if prefixLen != 1 {
		t.Fatalf("unexpected prefix length for argument completion: got=%d want=1", prefixLen)
	}

	foundDocs := false
	for _, s := range suggestions {
		if string(s) == "ocs" {
			foundDocs = true
			break
		}
	}
	if !foundDocs {
		t.Fatalf("expected file completion suggestions to include docs suffix")
	}
}
