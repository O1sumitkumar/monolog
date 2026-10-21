// Package launch opens a task in an external program (Claude Code in a new
// terminal tab, by default) from the TUI's `o` key.
//
// It resolves the working directory from the task's tags, builds the prompt
// and substitutes both into the configured argv. The package must not import
// internal/config: callers snapshot config.Launch() and pass values in.
package launch

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/maksmas/monolog/internal/model"
)

// provenanceTag marks tasks filed by the Claude Code skill. It says who filed
// a task, not which repo it belongs to, so it never selects a directory.
const provenanceTag = "claude"

// ResolveDir picks the launch directory for a task. Tags are tried in the
// task's own order and the first one present in dirs wins; the reserved
// active tag and the claude provenance tag are skipped even when mapped. With
// no match it falls back to defaultDir, and errors when that is empty too. A
// leading ~ in the result is expanded to the user's home directory.
func ResolveDir(tags []string, dirs map[string]string, defaultDir string) (string, error) {
	for _, tag := range tags {
		if tag == model.ActiveTag || tag == provenanceTag {
			continue
		}
		if dir, ok := dirs[tag]; ok && dir != "" {
			return expandHome(dir)
		}
	}
	if defaultDir != "" {
		return expandHome(defaultDir)
	}
	return "", fmt.Errorf("no launch dir for tags %v", tags)
}

// expandHome replaces a leading "~" or "~/" with the home directory. "~user"
// and a "~" anywhere else in the path are left alone.
func expandHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("expand %s: %w", path, err)
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~")), nil
}

// BuildPrompt renders the fixed prompt handed to Claude Code. The body is
// appended after a blank line only when it has non-whitespace content.
func BuildPrompt(id, title, body string) string {
	prompt := "Lets work on monolog item " + id + " " + title
	if strings.TrimSpace(body) == "" {
		return prompt
	}
	return prompt + "\n\n" + strings.TrimRight(body, " \t\r\n")
}

// BuildArgv substitutes {dir} and {prompt} into every element of command and
// returns a new slice. Substitution is a single pass, so a literal "{dir}"
// inside the prompt is not re-substituted. No shell is involved: the prompt
// lands in exactly one argv element however many quotes or newlines it holds.
func BuildArgv(command []string, dir, prompt string) []string {
	r := strings.NewReplacer("{dir}", dir, "{prompt}", prompt)
	argv := make([]string, len(command))
	for i, el := range command {
		argv[i] = r.Replace(el)
	}
	return argv
}
