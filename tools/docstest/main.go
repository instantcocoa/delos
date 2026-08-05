// Command docstest executes the documentation.
//
// Every fenced bash block in the docs whose first line is the marker comment
//
//	# docs-test
//
// is extracted and run in a scratch directory. If a documented command stops
// working, this fails like any other test. Blocks that need Docker, provider
// credentials, or a running stack simply do not carry the marker.
//
// Usage:
//
//	go run ./tools/docstest [files...]     # defaults to the standard doc set
//
// Marker options (space separated, on the marker line):
//
//	# docs-test              the block must exit 0
//	# docs-test expect-fail  the block must exit non-zero (documented failures)
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// block is one extracted, runnable documentation snippet.
type block struct {
	File       string
	Line       int // 1-based line of the opening fence
	Body       string
	ExpectFail bool
}

const marker = "# docs-test"

func main() {
	files := os.Args[1:]
	if len(files) == 0 {
		files = defaultFiles()
	}

	repoRoot, err := os.Getwd()
	if err != nil {
		fatal(err)
	}

	var blocks []block
	for _, f := range files {
		found, err := extract(f)
		if err != nil {
			fatal(err)
		}
		blocks = append(blocks, found...)
	}

	if len(blocks) == 0 {
		fmt.Println("docstest: no blocks marked with `" + marker + "` were found")
		os.Exit(1)
	}

	failures := 0
	for _, b := range blocks {
		start := time.Now()
		out, err := run(b, repoRoot)
		label := fmt.Sprintf("%s:%d", b.File, b.Line)
		switch {
		case b.ExpectFail && err == nil:
			failures++
			fmt.Printf("FAIL  %s  (expected a non-zero exit, got success)\n%s\n", label, indent(out))
		case !b.ExpectFail && err != nil:
			failures++
			fmt.Printf("FAIL  %s  (%v)\n%s\n", label, err, indent(out))
		default:
			fmt.Printf("ok    %s  (%s)\n", label, time.Since(start).Round(time.Millisecond))
		}
	}

	fmt.Printf("\n%d block(s) run, %d failed\n", len(blocks), failures)
	if failures > 0 {
		os.Exit(1)
	}
}

// defaultFiles is the documentation set that must stay executable.
func defaultFiles() []string {
	var files []string
	candidates := []string{"README.md", "GETTING_STARTED.md", "TROUBLESHOOTING.md"}
	for _, pattern := range []string{"docs/*.md", "docs/getting-started/*.md", "docs/guides/*.md"} {
		matches, _ := filepath.Glob(pattern)
		candidates = append(candidates, matches...)
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			files = append(files, c)
		}
	}
	return files
}

// extract pulls the marked bash blocks out of a Markdown file.
func extract(path string) ([]block, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("docstest: %w", err)
	}
	lines := strings.Split(string(data), "\n")

	var blocks []block
	for i := 0; i < len(lines); i++ {
		fence := strings.TrimSpace(lines[i])
		if fence != "```bash" && fence != "```sh" && fence != "```shell" {
			continue
		}
		// Find the closing fence.
		end := -1
		for j := i + 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == "```" {
				end = j
				break
			}
		}
		if end < 0 {
			return nil, fmt.Errorf("%s:%d: unterminated code fence", path, i+1)
		}
		body := lines[i+1 : end]
		i = end

		if len(body) == 0 || !strings.HasPrefix(strings.TrimSpace(body[0]), marker) {
			continue
		}
		opts := strings.Fields(strings.TrimPrefix(strings.TrimSpace(body[0]), marker))
		b := block{File: path, Line: i + 1, Body: strings.Join(body[1:], "\n")}
		for _, opt := range opts {
			switch opt {
			case "expect-fail":
				b.ExpectFail = true
			default:
				return nil, fmt.Errorf("%s:%d: unknown docs-test option %q", path, i+1, opt)
			}
		}
		blocks = append(blocks, b)
	}
	return blocks, nil
}

// run executes a block in a scratch directory with the repo's bin/ on PATH.
func run(b block, repoRoot string) (string, error) {
	dir, err := os.MkdirTemp("", "docstest-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)

	// Example files are copied in so that documented commands can refer to
	// them by the same relative path a reader would use from a clone.
	examples, _ := filepath.Glob(filepath.Join(repoRoot, "*.example"))
	for _, src := range examples {
		data, readErr := os.ReadFile(src)
		if readErr != nil {
			continue
		}
		if writeErr := os.WriteFile(filepath.Join(dir, filepath.Base(src)), data, 0o600); writeErr != nil {
			return "", writeErr
		}
	}

	script := "set -euo pipefail\n" + b.Body + "\n"
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"REPO_ROOT="+repoRoot,
		"PATH="+filepath.Join(repoRoot, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
		// Documented commands must never touch a developer's real stack.
		"DELOS_CONFIG=",
		"NO_COLOR=1",
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func indent(s string) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return "      (no output)"
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = "      " + l
	}
	return strings.Join(lines, "\n")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "docstest:", err)
	os.Exit(1)
}
