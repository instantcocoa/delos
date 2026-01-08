// Package output provides output formatting for the CLI.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"gopkg.in/yaml.v3"
)

// Format represents an output format.
type Format string

const (
	FormatTable Format = "table"
	FormatJSON  Format = "json"
	FormatYAML  Format = "yaml"
)

// Writer handles formatted output.
type Writer struct {
	format Format
	out    io.Writer
}

// NewWriter creates a new output writer.
func NewWriter(format string) *Writer {
	f := Format(format)
	if f != FormatJSON && f != FormatYAML {
		f = FormatTable
	}
	return &Writer{
		format: f,
		out:    os.Stdout,
	}
}

// Print outputs data in the configured format.
func (w *Writer) Print(data interface{}) error {
	switch w.format {
	case FormatJSON:
		return w.printJSON(data)
	case FormatYAML:
		return w.printYAML(data)
	default:
		return w.printTable(data)
	}
}

func (w *Writer) printJSON(data interface{}) error {
	enc := json.NewEncoder(w.out)
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}

func (w *Writer) printYAML(data interface{}) error {
	enc := yaml.NewEncoder(w.out)
	enc.SetIndent(2)
	return enc.Encode(data)
}

func (w *Writer) printTable(data interface{}) error {
	// For table format, we need to handle specific types
	switch v := data.(type) {
	case Table:
		return w.writeTable(v)
	default:
		// Fall back to JSON for complex types
		return w.printJSON(data)
	}
}

// Table represents tabular data.
type Table struct {
	Headers []string
	Rows    [][]string
}

func (w *Writer) writeTable(t Table) error {
	tw := tabwriter.NewWriter(w.out, 0, 0, 2, ' ', 0)

	// Write headers
	for i, h := range t.Headers {
		if i > 0 {
			fmt.Fprint(tw, "\t")
		}
		fmt.Fprint(tw, h)
	}
	fmt.Fprintln(tw)

	// Write rows
	for _, row := range t.Rows {
		for i, cell := range row {
			if i > 0 {
				fmt.Fprint(tw, "\t")
			}
			fmt.Fprint(tw, cell)
		}
		fmt.Fprintln(tw)
	}

	return tw.Flush()
}

// Success prints a success message.
func Success(format string, args ...interface{}) {
	fmt.Printf("✓ "+format+"\n", args...)
}

// Error prints an error message.
func Error(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "✗ "+format+"\n", args...)
}

// Info prints an info message.
func Info(format string, args ...interface{}) {
	fmt.Printf("→ "+format+"\n", args...)
}
