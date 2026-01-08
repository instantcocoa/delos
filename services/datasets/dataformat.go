package datasets

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"github.com/parquet-go/parquet-go"
)

// Row represents a single row of data with named fields.
type Row map[string]interface{}

// Parser reads data in a specific format and returns rows.
type Parser interface {
	// Parse reads from the reader and returns rows through a channel.
	// The channel is closed when parsing is complete or on error.
	Parse(r io.Reader, opts CSVOptions) (<-chan Row, <-chan error)
}

// Writer writes rows in a specific format.
type Writer interface {
	// Write writes rows to the writer.
	Write(w io.Writer, rows []Row, opts CSVOptions) error
}

// NewParser creates a parser for the given format.
func NewParser(format DataFormat) (Parser, error) {
	switch format {
	case DataFormatCSV:
		return &CSVParser{}, nil
	case DataFormatJSONL:
		return &JSONLParser{}, nil
	case DataFormatJSON:
		return &JSONParser{}, nil
	case DataFormatParquet:
		return &ParquetParser{}, nil
	default:
		return nil, fmt.Errorf("unsupported format: %v", format)
	}
}

// NewWriter creates a writer for the given format.
func NewWriter(format DataFormat) (Writer, error) {
	switch format {
	case DataFormatCSV:
		return &CSVWriter{}, nil
	case DataFormatJSONL:
		return &JSONLWriter{}, nil
	case DataFormatJSON:
		return &JSONWriter{}, nil
	case DataFormatParquet:
		return &ParquetWriter{}, nil
	default:
		return nil, fmt.Errorf("unsupported format: %v", format)
	}
}

// CSVParser parses CSV data.
type CSVParser struct{}

// Parse reads CSV data and returns rows.
func (p *CSVParser) Parse(r io.Reader, opts CSVOptions) (<-chan Row, <-chan error) {
	rows := make(chan Row, 100)
	errs := make(chan error, 1)

	go func() {
		defer close(rows)
		defer close(errs)

		reader := csv.NewReader(r)
		if opts.Delimiter != 0 {
			reader.Comma = opts.Delimiter
		}
		reader.LazyQuotes = true
		reader.TrimLeadingSpace = true

		// Read header if present
		var headers []string
		if opts.HasHeader {
			var err error
			headers, err = reader.Read()
			if err != nil {
				errs <- fmt.Errorf("failed to read CSV header: %w", err)
				return
			}
		}

		rowNum := 0
		for {
			record, err := reader.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				errs <- fmt.Errorf("failed to read CSV row %d: %w", rowNum, err)
				return
			}
			rowNum++

			row := make(Row)
			for i, value := range record {
				var key string
				if headers != nil && i < len(headers) {
					key = headers[i]
				} else {
					key = fmt.Sprintf("col%d", i)
				}

				// Try to infer type
				row[key] = inferType(value)
			}

			rows <- row
		}
	}()

	return rows, errs
}

// inferType tries to convert a string to a more specific type.
func inferType(s string) interface{} {
	// Try int
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	// Try float
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	// Try bool
	if b, err := strconv.ParseBool(s); err == nil {
		return b
	}
	// Default to string
	return s
}

// CSVWriter writes data as CSV.
type CSVWriter struct{}

// Write writes rows as CSV.
func (w *CSVWriter) Write(wr io.Writer, rows []Row, opts CSVOptions) error {
	if len(rows) == 0 {
		return nil
	}

	writer := csv.NewWriter(wr)
	if opts.Delimiter != 0 {
		writer.Comma = opts.Delimiter
	}
	defer writer.Flush()

	// Collect all unique headers
	headerSet := make(map[string]bool)
	for _, row := range rows {
		for k := range row {
			headerSet[k] = true
		}
	}

	headers := make([]string, 0, len(headerSet))
	for k := range headerSet {
		headers = append(headers, k)
	}

	// Write header if requested
	if opts.HasHeader {
		if err := writer.Write(headers); err != nil {
			return fmt.Errorf("failed to write CSV header: %w", err)
		}
	}

	// Write rows
	for i, row := range rows {
		record := make([]string, len(headers))
		for j, h := range headers {
			if v, ok := row[h]; ok {
				record[j] = fmt.Sprintf("%v", v)
			}
		}
		if err := writer.Write(record); err != nil {
			return fmt.Errorf("failed to write CSV row %d: %w", i, err)
		}
	}

	return nil
}

// JSONLParser parses JSON Lines data (one JSON object per line).
type JSONLParser struct{}

// Parse reads JSONL data and returns rows.
func (p *JSONLParser) Parse(r io.Reader, opts CSVOptions) (<-chan Row, <-chan error) {
	rows := make(chan Row, 100)
	errs := make(chan error, 1)

	go func() {
		defer close(rows)
		defer close(errs)

		scanner := bufio.NewScanner(r)
		// Increase buffer size for large JSON objects
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 10*1024*1024) // 10MB max line size

		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}

			var row Row
			if err := json.Unmarshal(line, &row); err != nil {
				errs <- fmt.Errorf("failed to parse JSON on line %d: %w", lineNum, err)
				return
			}

			rows <- row
		}

		if err := scanner.Err(); err != nil {
			errs <- fmt.Errorf("error reading JSONL: %w", err)
		}
	}()

	return rows, errs
}

// JSONLWriter writes data as JSON Lines.
type JSONLWriter struct{}

// Write writes rows as JSONL.
func (w *JSONLWriter) Write(wr io.Writer, rows []Row, opts CSVOptions) error {
	encoder := json.NewEncoder(wr)
	for i, row := range rows {
		if err := encoder.Encode(row); err != nil {
			return fmt.Errorf("failed to encode row %d: %w", i, err)
		}
	}
	return nil
}

// JSONParser parses a JSON array of objects.
type JSONParser struct{}

// Parse reads JSON array data and returns rows.
func (p *JSONParser) Parse(r io.Reader, opts CSVOptions) (<-chan Row, <-chan error) {
	rows := make(chan Row, 100)
	errs := make(chan error, 1)

	go func() {
		defer close(rows)
		defer close(errs)

		var data []Row
		decoder := json.NewDecoder(r)
		if err := decoder.Decode(&data); err != nil {
			errs <- fmt.Errorf("failed to parse JSON array: %w", err)
			return
		}

		for _, row := range data {
			rows <- row
		}
	}()

	return rows, errs
}

// JSONWriter writes data as a JSON array.
type JSONWriter struct{}

// Write writes rows as a JSON array.
func (w *JSONWriter) Write(wr io.Writer, rows []Row, opts CSVOptions) error {
	encoder := json.NewEncoder(wr)
	encoder.SetIndent("", "  ")
	return encoder.Encode(rows)
}

// ParquetParser parses Parquet data.
type ParquetParser struct{}

// Parse reads Parquet data and returns rows.
func (p *ParquetParser) Parse(r io.Reader, opts CSVOptions) (<-chan Row, <-chan error) {
	rows := make(chan Row, 100)
	errs := make(chan error, 1)

	go func() {
		defer close(rows)
		defer close(errs)

		// Read all data into memory (parquet-go requires io.ReaderAt)
		data, err := io.ReadAll(r)
		if err != nil {
			errs <- fmt.Errorf("failed to read parquet data: %w", err)
			return
		}

		// Open parquet file
		file, err := parquet.OpenFile(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			errs <- fmt.Errorf("failed to open parquet file: %w", err)
			return
		}

		// Read rows using the generic reader
		rowReader := parquet.NewGenericReader[map[string]any](file)
		defer rowReader.Close()

		buffer := make([]map[string]any, 100)
		for {
			n, err := rowReader.Read(buffer)
			if err != nil && err != io.EOF {
				errs <- fmt.Errorf("failed to read parquet rows: %w", err)
				return
			}

			for j := 0; j < n; j++ {
				row := make(Row)
				for k, v := range buffer[j] {
					row[k] = v
				}
				rows <- row
			}

			if err == io.EOF || n == 0 {
				break
			}
		}
	}()

	return rows, errs
}

// ParquetWriter writes data as Parquet.
type ParquetWriter struct{}

// Write writes rows as Parquet.
func (w *ParquetWriter) Write(wr io.Writer, rows []Row, opts CSVOptions) error {
	if len(rows) == 0 {
		return nil
	}

	// Convert rows to generic maps
	genericRows := make([]map[string]any, len(rows))
	for i, row := range rows {
		genericRows[i] = map[string]any(row)
	}

	// Create a buffer to write to
	var buf bytes.Buffer
	writer := parquet.NewGenericWriter[map[string]any](&buf)

	// Write rows
	_, err := writer.Write(genericRows)
	if err != nil {
		return fmt.Errorf("failed to write parquet rows: %w", err)
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("failed to close parquet writer: %w", err)
	}

	// Copy buffer to output writer
	_, err = io.Copy(wr, &buf)
	return err
}
