package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestMainHelpFlag(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "run", ".", "-h")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()

	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("metaserver main help command timed out")
	}

	output := string(out)
	if err != nil && !strings.Contains(strings.ToLower(output), "usage") {
		t.Fatalf("expected help usage output, got err=%v output=%s", err, output)
	}

	if !strings.Contains(output, "-state_file") {
		t.Fatalf("expected help output to include -state_file flag, got: %s", output)
	}
}
