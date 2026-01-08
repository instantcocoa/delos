// Package datasets provides test data management for LLM applications.
package datasets

import (
	"time"
)

// ExampleSource indicates how an example was created.
type ExampleSource int

const (
	ExampleSourceUnspecified ExampleSource = iota
	ExampleSourceManual
	ExampleSourceGenerated
	ExampleSourceProduction
	ExampleSourceImported
)

// DataFormat specifies the format of external data.
type DataFormat int

const (
	DataFormatUnspecified DataFormat = iota
	DataFormatCSV
	DataFormatJSONL
	DataFormatParquet
	DataFormatJSON
)

// Dataset represents a collection of test examples.
type Dataset struct {
	ID           string
	Name         string
	Description  string
	PromptID     string // linked prompt
	Schema       DatasetSchema
	ExampleCount int
	LastUpdated  time.Time
	Tags         []string
	Metadata     map[string]string
	Version      int
	CreatedBy    string
	CreatedAt    time.Time
}

// DatasetSchema defines the structure of examples.
type DatasetSchema struct {
	InputFields          []SchemaField
	ExpectedOutputFields []SchemaField
}

// SchemaField describes a field in the schema.
type SchemaField struct {
	Name        string
	Type        string // string, number, boolean, json, array
	Description string
	Required    bool
}

// Example represents a single test case.
type Example struct {
	ID             string
	DatasetID      string
	Input          map[string]interface{}
	ExpectedOutput map[string]interface{}
	Metadata       map[string]string
	Source         ExampleSource
	CreatedAt      time.Time
}

// CreateDatasetInput contains input for creating a dataset.
type CreateDatasetInput struct {
	Name        string
	Description string
	PromptID    string
	Schema      DatasetSchema
	Tags        []string
	Metadata    map[string]string
	CreatedBy   string
}

// UpdateDatasetInput contains input for updating a dataset.
type UpdateDatasetInput struct {
	ID          string
	Name        string
	Description string
	Tags        []string
	Metadata    map[string]string
}

// ListDatasetsQuery contains filters for listing datasets.
type ListDatasetsQuery struct {
	PromptID string
	Tags     []string
	Search   string
	Limit    int
	Offset   int
}

// AddExamplesInput contains input for adding examples.
type AddExamplesInput struct {
	DatasetID string
	Examples  []ExampleInput
}

// ExampleInput contains input for a single example.
type ExampleInput struct {
	Input          map[string]interface{}
	ExpectedOutput map[string]interface{}
	Metadata       map[string]string
	Source         ExampleSource
}

// GetExamplesQuery contains filters for getting examples.
type GetExamplesQuery struct {
	DatasetID string
	Limit     int
	Offset    int
	Shuffle   bool
}

// GenerateExamplesInput contains input for generating examples.
type GenerateExamplesInput struct {
	DatasetID        string
	Count            int
	GenerationPrompt string
	SeedExamples     []Example
}

// DataSource specifies where external data is located.
type DataSource struct {
	LocalFile *LocalFileSource
	S3        *S3Source
	GCS       *GCSSource
	URL       *URLSource
	Inline    *InlineSource
}

// LocalFileSource reads from local filesystem.
type LocalFileSource struct {
	Path string
}

// S3Source reads from Amazon S3.
type S3Source struct {
	Bucket          string
	Key             string
	Region          string
	Endpoint        string // Custom endpoint for S3-compatible stores
	AccessKeyID     string
	SecretAccessKey string
}

// GCSSource reads from Google Cloud Storage.
type GCSSource struct {
	Bucket    string
	Object    string
	ProjectID string
}

// URLSource reads from an HTTP(S) URL.
type URLSource struct {
	URL     string
	Headers map[string]string
}

// InlineSource provides data directly.
type InlineSource struct {
	Data   []byte
	Format DataFormat
}

// ColumnMapping maps source columns to dataset schema fields.
type ColumnMapping struct {
	SourceColumn string
	TargetField  string
	IsInput      bool // true = input field, false = expected_output field
}

// CSVOptions contains CSV-specific parsing options.
type CSVOptions struct {
	Delimiter  rune
	HasHeader  bool
	QuoteChar  rune
	EscapeChar rune
	Encoding   string
}

// DefaultCSVOptions returns sensible defaults for CSV parsing.
func DefaultCSVOptions() CSVOptions {
	return CSVOptions{
		Delimiter:  ',',
		HasHeader:  true,
		QuoteChar:  '"',
		EscapeChar: '\\',
		Encoding:   "utf-8",
	}
}

// ImportExamplesInput contains input for importing examples.
type ImportExamplesInput struct {
	DatasetID      string
	Source         DataSource
	Format         DataFormat
	ColumnMappings []ColumnMapping
	CSVOptions     CSVOptions
	SkipInvalid    bool
	MaxRows        int
}

// ImportResult contains the result of an import operation.
type ImportResult struct {
	ImportedCount int
	SkippedCount  int
	ErrorCount    int
	Errors        []ImportError
}

// ImportError describes an error during import.
type ImportError struct {
	RowNumber    int
	ErrorMessage string
	RawData      string
}

// ExportExamplesInput contains input for exporting examples.
type ExportExamplesInput struct {
	DatasetID   string
	Format      DataFormat
	Destination *DataSource // nil means return inline
	CSVOptions  CSVOptions
	Limit       int
	Offset      int
}

// ExportResult contains the result of an export operation.
type ExportResult struct {
	Data           []byte
	Format         DataFormat
	ExportedCount  int
	DestinationURI string // Set if exported to external destination
}
