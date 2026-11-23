// Command skipguard runs `go test -json` and fails the run when a
// database-backed test skips instead of running.
//
// # Why it exists
//
// Delos's database-backed tests skip when no `delos_test` database is
// reachable (see services/runtime/keys_postgres_test.go,
// services/prompt/store_test.go and pkg/database/database_integration_test.go).
// `go test` reports a skip as `ok`, so a machine without Postgres silently
// passes a suite it never actually exercised. In CI Postgres is expected to
// be up, so a skip there means the database path did not run and the build
// must fail. Local development still uses plain `make test` / `make
// test-unit`, which keep the skip-friendly behavior.
//
// # How it is triggered
//
// CI runs this tool with DELOS_TEST_REQUIRE_DB=1 (see
// .github/workflows/ci.yaml). Without that variable the tool is a faithful
// `go test` wrapper: it re-emits output, propagates the test exit code, and
// only warns about database skips. With it set, any skip in a database-backed
// package turns into a nonzero exit and a list of exactly what skipped.
//
// # Scope
//
// The database-backed packages are fixed below. A skip in any other package
// (Ollama availability, -short mode, missing live provider keys) is left
// alone: only skips in packages whose tests require Postgres are fatal.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// dbPackages are the packages whose tests need Postgres. They are the full
// import paths so they can be matched against `go test -json` Package fields.
var dbPackages = []string{
	"github.com/instantcocoa/delos/pkg/database",
	"github.com/instantcocoa/delos/services/prompt",
	"github.com/instantcocoa/delos/services/runtime",
}

// testEvent is the subset of a `go test -json` event we care about.
type testEvent struct {
	Time    string  `json:"Time"`
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Output  string  `json:"Output"`
	Elapsed float64 `json:"Elapsed"`
}

func main() {
	requireDB := envBool("DELOS_TEST_REQUIRE_DB")

	// Everything after the program name is passed to `go test` unchanged,
	// so `-race -coverpkg=./... -coverprofile=coverage.out -covermode=atomic
	// ./...` works exactly as it did on the raw `go test` invocation.
	goTestArgs := append([]string{"test", "-json", "-count=1"}, os.Args[1:]...)
	if len(os.Args) == 1 {
		goTestArgs = append(goTestArgs, "./...")
	}

	cmd := exec.Command("go", goTestArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fatalf("skipguard: opening go test output pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		fatalf("skipguard: starting go test: %v", err)
	}

	var dbSkips []string
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1<<20), 8<<20)
	for scanner.Scan() {
		var ev testEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			// Not a JSON event (should not happen under -json): pass it through.
			fmt.Println(scanner.Text())
			continue
		}
		if ev.Output != "" {
			fmt.Print(ev.Output)
		}
		if ev.Action == "skip" && ev.Test != "" && isDBPackage(ev.Package) {
			dbSkips = append(dbSkips, ev.Package+" → "+ev.Test)
		}
	}
	if err := scanner.Err(); err != nil {
		fatalf("skipguard: reading go test output: %v", err)
	}

	waitErr := cmd.Wait()
	goExit := exitCode(waitErr)

	if len(dbSkips) > 0 {
		fmt.Fprintf(os.Stderr, "\nskipguard: %d database-backed test(s) skipped:\n", len(dbSkips))
		for _, s := range dbSkips {
			fmt.Fprintf(os.Stderr, "  - %s\n", s)
		}
		if requireDB {
			fmt.Fprintf(os.Stderr, "skipguard: DELOS_TEST_REQUIRE_DB=%s - failing because database-backed tests did not run (Postgres missing or unreachable in CI)\n", os.Getenv("DELOS_TEST_REQUIRE_DB"))
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "skipguard: DELOS_TEST_REQUIRE_DB is unset, so these skips are tolerated (set DELOS_TEST_REQUIRE_DB=1 to fail on them)")
	}

	os.Exit(goExit)
}

func isDBPackage(pkg string) bool {
	for _, p := range dbPackages {
		if pkg == p {
			return true
		}
	}
	return false
}

func envBool(name string) bool {
	v := os.Getenv(name)
	return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	fmt.Fprintf(os.Stderr, "skipguard: go test failed: %v\n", err)
	return 1
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
