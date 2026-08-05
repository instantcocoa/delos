package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/instantcocoa/delos/services/runtime"
)

func TestHumanDuration(t *testing.T) {
	cases := map[float64]string{
		0:      "-",
		0.4:    "400us",
		12.4:   "12ms",
		1250:   "1.2s",
		90_000: "1.5m",
	}
	for ms, want := range cases {
		if got := humanDuration(ms); got != want {
			t.Errorf("humanDuration(%v) = %q, want %q", ms, got, want)
		}
	}
}

func TestHumanCost(t *testing.T) {
	cases := map[float64]string{
		0:       "-",
		0.00042: "$0.00042",
		0.42:    "$0.4200",
		12.5:    "$12.50",
	}
	for usd, want := range cases {
		if got := humanCost(usd); got != want {
			t.Errorf("humanCost(%v) = %q, want %q", usd, got, want)
		}
	}
}

func TestTailLineColumnsAlign(t *testing.T) {
	ev := runtime.TailEvent{
		Time:             time.Date(2026, 8, 3, 10, 30, 0, 0, time.UTC),
		Model:            "gpt-4o",
		Provider:         "openai",
		Status:           200,
		LatencyMS:        1234,
		PromptTokens:     10,
		CompletionTokens: 5,
		CostUSD:          0.0004,
		KeyName:          "ci",
	}
	line := tailLine(ev)
	header := tailHeader()
	for _, want := range []string{"gpt-4o", "openai", "200", "1.2s", "10->5", "$0.00040", "ci"} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q is missing %q", line, want)
		}
	}
	// The header and a row must line up so the output reads as a table.
	if got, want := strings.Index(line, "openai"), strings.Index(header, "PROVIDER"); got != want {
		t.Errorf("provider column at %d, header at %d\n%s\n%s", got, want, header, line)
	}
}

func TestTailLineErrorEvent(t *testing.T) {
	ev := runtime.TailEvent{
		Time:   time.Now(),
		Model:  "nope",
		Status: 404,
		Error:  "model_not_found: no provider serves \"nope\"",
	}
	line := tailLine(ev)
	if !strings.Contains(line, "404") || !strings.Contains(line, "model_not_found") {
		t.Errorf("error line = %q", line)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("abcdefghij", 5); got != "ab..." {
		t.Errorf("truncate = %q", got)
	}
	if got := truncate("abc", 5); got != "abc" {
		t.Errorf("truncate = %q", got)
	}
}
