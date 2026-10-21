package launch

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestResolveDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dirs := map[string]string{
		"paperwall": "/src/paperwall",
		"monolog":   "/src/monolog",
		"active":    "/src/active",
		"claude":    "/src/claude",
		"tilde":     "~",
		"tildesub":  "~/IdeaProjects/x",
		"otheruser": "~bob/x",
		"midpath":   "/src/~/x",
		"empty":     "",
	}

	cases := []struct {
		name       string
		tags       []string
		defaultDir string
		want       string
	}{
		{"first matching tag wins by task order", []string{"monolog", "paperwall"}, "", "/src/monolog"},
		{"order reversed picks the other", []string{"paperwall", "monolog"}, "", "/src/paperwall"},
		{"unmapped tags skipped before a match", []string{"inbox", "paperwall"}, "", "/src/paperwall"},
		{"active skipped even if mapped", []string{"active", "monolog"}, "", "/src/monolog"},
		{"claude skipped even if mapped", []string{"claude", "paperwall"}, "", "/src/paperwall"},
		{"only reserved tags fall back to default", []string{"active", "claude"}, "/src/default", "/src/default"},
		{"unmapped tags fall back to default", []string{"inbox"}, "/src/default", "/src/default"},
		{"no tags fall back to default", nil, "/src/default", "/src/default"},
		{"bare tilde expanded", []string{"tilde"}, "", home},
		{"tilde slash expanded", []string{"tildesub"}, "", home + "/IdeaProjects/x"},
		{"default dir tilde expanded", []string{"inbox"}, "~/IdeaProjects", home + "/IdeaProjects"},
		{"mid-path tilde untouched", []string{"midpath"}, "", "/src/~/x"},
		{"empty mapping falls through to next tag", []string{"empty", "monolog"}, "", "/src/monolog"},
		{"empty mapping falls back to default", []string{"empty"}, "/src/default", "/src/default"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveDir(tc.tags, dirs, tc.defaultDir)
			if err != nil {
				t.Fatalf("ResolveDir error: %v", err)
			}
			if got != tc.want {
				t.Errorf("ResolveDir = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveDir_NoMatchNoDefault(t *testing.T) {
	tags := []string{"inbox", "claude"}
	_, err := ResolveDir(tags, map[string]string{"monolog": "/src/monolog"}, "")
	if err == nil {
		t.Fatal("expected error")
	}
	if want := "no launch dir for tags [inbox claude]"; err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestResolveDir_Errors(t *testing.T) {
	cases := []struct {
		name       string
		tags       []string
		dirs       map[string]string
		defaultDir string
		home       string
		want       string
	}{
		{"relative mapping rejected", []string{"p"}, map[string]string{"p": "src/p"}, "", "/home/u", "launch dir must be absolute: src/p"},
		{"relative default rejected", []string{"x"}, nil, "./repos", "/home/u", "launch dir must be absolute: ./repos"},
		{"tilde user is not expanded, so it is relative", []string{"otheruser"}, map[string]string{"otheruser": "~bob/x"}, "", "/home/u", "launch dir must be absolute: ~bob/x"},
		{"unset HOME fails tilde expansion", []string{"p"}, map[string]string{"p": "~/x"}, "", "", "launch dir ~/x: "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", tc.home)
			_, err := ResolveDir(tc.tags, tc.dirs, tc.defaultDir)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("ResolveDir error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestExpandHome_LeavesTildeUserAlone(t *testing.T) {
	got, err := expandHome("~bob/x")
	if err != nil || got != "~bob/x" {
		t.Errorf("expandHome(~bob/x) = %q, %v; want it untouched", got, err)
	}
}

func TestResolveDir_NilDirs(t *testing.T) {
	got, err := ResolveDir([]string{"monolog"}, nil, "/src/default")
	if err != nil {
		t.Fatalf("ResolveDir error: %v", err)
	}
	if got != "/src/default" {
		t.Errorf("ResolveDir = %q, want /src/default", got)
	}
}

func TestBuildPrompt(t *testing.T) {
	const id = "01J5K3ABCDEFGHJKMNPQRSTVWX"
	cases := []struct {
		name  string
		title string
		body  string
		want  string
	}{
		{"empty body", "Fix login bug", "", "Lets work on monolog item " + id + " Fix login bug"},
		{"whitespace body", "Fix login bug", " \n\t\n", "Lets work on monolog item " + id + " Fix login bug"},
		{"single line body", "Fix", "monolog/cmd/add.go:12", "Lets work on monolog item " + id + " Fix\n\nmonolog/cmd/add.go:12"},
		{"multi-line body preserved, trailing newlines trimmed", "Fix", "line one\n\nline two\n  indented\n\n", "Lets work on monolog item " + id + " Fix\n\nline one\n\nline two\n  indented"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := BuildPrompt(id, tc.title, tc.body); got != tc.want {
				t.Errorf("BuildPrompt = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildArgv(t *testing.T) {
	command := []string{"kitty", "@", "launch", "--type=tab", "--cwd={dir}", "claude", "{prompt}"}
	nasty := "it's \"quoted\"\nline two `whoami` $(rm -rf ~) {dir} {prompt}"

	cases := []struct {
		name   string
		dir    string
		prompt string
		want   []string
	}{
		{
			name:   "placeholders replaced including mid-string",
			dir:    "/src/monolog",
			prompt: "hello",
			want:   []string{"kitty", "@", "launch", "--type=tab", "--cwd=/src/monolog", "claude", "hello"},
		},
		{
			name:   "hostile prompt survives as one exact element, not re-substituted",
			dir:    "/src/x",
			prompt: nasty,
			want:   []string{"kitty", "@", "launch", "--type=tab", "--cwd=/src/x", "claude", nasty},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			orig := append([]string(nil), command...)
			got := BuildArgv(command, tc.dir, tc.prompt)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("BuildArgv = %q, want %q", got, tc.want)
			}
			if !reflect.DeepEqual(command, orig) {
				t.Errorf("input slice mutated: %q", command)
			}
		})
	}
}

func TestBuildArgv_MultiplePlaceholdersInOneElement(t *testing.T) {
	got := BuildArgv([]string{"wezterm", "--cwd {dir} -- {prompt}"}, "/d", "p {dir}")
	want := []string{"wezterm", "--cwd /d -- p {dir}"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuildArgv = %q, want %q", got, want)
	}
}

func TestBuildArgv_ReturnsNewSlice(t *testing.T) {
	command := []string{"echo", "plain"}
	got := BuildArgv(command, "/d", "p")
	if !reflect.DeepEqual(got, command) {
		t.Fatalf("BuildArgv = %q, want unchanged %q", got, command)
	}
	got[0] = "changed"
	if command[0] != "echo" {
		t.Error("BuildArgv result aliases the input slice")
	}
	if strings.Join(BuildArgv(nil, "/d", "p"), "") != "" {
		t.Error("nil command should give an empty argv")
	}
}

// requireSh skips tests that exec a real shell where none is available.
func requireSh(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("launch.Run is unix-only")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}
}

func TestRunSuccess(t *testing.T) {
	requireSh(t)
	if err := Run(context.Background(), []string{"sh", "-c", "exit 0"}, t.TempDir()); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRunFailureCarriesOutput(t *testing.T) {
	requireSh(t)
	err := Run(context.Background(), []string{"sh", "-c", "echo; echo boom >&2; echo second >&2; exit 3"}, t.TempDir())
	if err == nil {
		t.Fatal("want error for exit 3")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error %q should carry the first output line", err)
	}
	if strings.Contains(err.Error(), "second") {
		t.Errorf("error %q should carry only the first output line", err)
	}
	if !strings.Contains(err.Error(), "exit status 3") {
		t.Errorf("error %q should wrap the exit status", err)
	}
}

func TestRunFailureWithoutOutput(t *testing.T) {
	requireSh(t)
	err := Run(context.Background(), []string{"sh", "-c", "exit 2"}, t.TempDir())
	if err == nil || err.Error() != "exit status 2" {
		t.Errorf("got %v, want bare exit status", err)
	}
}

func TestRunMissingBinary(t *testing.T) {
	if err := Run(context.Background(), []string{"monolog-no-such-launcher-xyz"}, t.TempDir()); err == nil {
		t.Fatal("want error for missing binary")
	}
}

func TestRunEmptyArgv(t *testing.T) {
	if err := Run(context.Background(), nil, t.TempDir()); err == nil {
		t.Fatal("want error for empty argv")
	}
}

func TestRunHonorsDir(t *testing.T) {
	requireSh(t)
	dir := t.TempDir()
	if err := Run(context.Background(), []string{"sh", "-c", "pwd > out"}, dir); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "out"))
	if err != nil {
		t.Fatalf("read out: %v", err)
	}
	want, _ := filepath.EvalSymlinks(dir)
	gotDir, _ := filepath.EvalSymlinks(strings.TrimSpace(string(got)))
	if gotDir != want {
		t.Errorf("child cwd = %q, want %q", gotDir, want)
	}
}

func TestRunPassesArgVerbatim(t *testing.T) {
	requireSh(t)
	dir := t.TempDir()
	arg := "Lets work on \"it\" 'now'\n\nbody with `ticks` and $(rm -rf ~) {dir}"
	if err := Run(context.Background(), []string{"sh", "-c", `printf %s "$1" > file`, "sh", arg}, dir); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "file"))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(got) != arg {
		t.Errorf("child got %q, want %q", got, arg)
	}
}

func TestRunContextTimeoutKills(t *testing.T) {
	requireSh(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := Run(ctx, []string{"sleep", "10"}, t.TempDir())
	if err == nil {
		t.Fatal("want error when the context deadline kills the child")
	}
	if !errors.Is(err, context.DeadlineExceeded) || !strings.HasPrefix(err.Error(), "launcher timed out") {
		t.Errorf("error = %v, want a launcher timed out error wrapping the deadline", err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond+waitDelay+time.Second {
		t.Errorf("Run took %v, should return shortly after the deadline", elapsed)
	}
}

func TestRunBackgroundChildHoldingPipe(t *testing.T) {
	requireSh(t)
	start := time.Now()
	if err := Run(context.Background(), []string{"sh", "-c", "sleep 3 & exit 0"}, t.TempDir()); err != nil {
		t.Fatalf("launcher exited 0, want nil error, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > waitDelay+2*time.Second {
		t.Errorf("Run took %v, should return within about WaitDelay", elapsed)
	}
}
