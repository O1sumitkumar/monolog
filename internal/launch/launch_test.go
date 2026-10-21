package launch

import (
	"reflect"
	"strings"
	"testing"
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
		{"tilde user untouched", []string{"otheruser"}, "", "~bob/x"},
		{"mid-path tilde untouched", []string{"midpath"}, "", "/src/~/x"},
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
