//go:build linux || darwin

package launch

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

const sessionHelperEnv = "MONOLOG_LAUNCH_SESSION_HELPER"

// TestHelperProcess is not a real test: TestRunStartsNewSession re-execs the
// test binary into it, and it records whether it leads its own session.
func TestHelperProcess(t *testing.T) {
	if os.Getenv(sessionHelperEnv) != "1" {
		return
	}
	sid, err := syscall.Getsid(0)
	leader := err == nil && sid == os.Getpid()
	_ = os.WriteFile("leader", []byte(strconv.FormatBool(leader)), 0o644)
	os.Exit(0)
}

// A child in its own session has no controlling terminal, whether or not the
// test process has one, so this discriminates in CI too.
func TestRunStartsNewSession(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable: %v", err)
	}
	t.Setenv(sessionHelperEnv, "1")
	dir := t.TempDir()
	if err := Run(context.Background(), []string{exe, "-test.run=^TestHelperProcess$"}, dir); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "leader"))
	if err != nil {
		t.Fatalf("helper wrote no result: %v", err)
	}
	if string(got) != "true" {
		t.Errorf("child session leader = %s, want true (Setsid not applied)", got)
	}
}
