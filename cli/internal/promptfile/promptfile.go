// Package promptfile implements the git-native Delos prompt file format.
//
// A prompt is a single YAML file named "<slug>.prompt.yaml". The repository is
// the source of truth; the control plane is an index and serving cache. The
// file format is described by a published JSON Schema (see
// docs/schemas/prompt.schema.json), which is embedded into the binary so that
// validation works offline.
package promptfile

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	promptv1 "github.com/instantcocoa/delos/gen/go/prompt/v1"
)

const (
	// Ext is the file extension (suffix) of a prompt file.
	Ext = ".prompt.yaml"

	// SchemaURL is the canonical published location of the JSON Schema.
	SchemaURL = "https://raw.githubusercontent.com/instantcocoa/delos/main/docs/schemas/prompt.schema.json"
)

// PromptFile is the on-disk representation of a prompt.
type PromptFile struct {
	Name        string            `yaml:"name" json:"name"`
	Slug        string            `yaml:"slug" json:"slug"`
	Description string            `yaml:"description,omitempty" json:"description,omitempty"`
	Messages    []Message         `yaml:"messages" json:"messages"`
	Variables   []Variable        `yaml:"variables,omitempty" json:"variables,omitempty"`
	Config      *Config           `yaml:"config,omitempty" json:"config,omitempty"`
	Tags        []string          `yaml:"tags,omitempty" json:"tags,omitempty"`
	Metadata    map[string]string `yaml:"metadata,omitempty" json:"metadata,omitempty"`
}

// Message is a single chat message in a prompt template.
type Message struct {
	Role    string `yaml:"role" json:"role"`
	Content string `yaml:"content" json:"content"`
}

// Variable declares a value that may be interpolated into message content.
type Variable struct {
	Name        string  `yaml:"name" json:"name"`
	Type        string  `yaml:"type,omitempty" json:"type,omitempty"`
	Required    bool    `yaml:"required,omitempty" json:"required,omitempty"`
	Description string  `yaml:"description,omitempty" json:"description,omitempty"`
	Default     *string `yaml:"default,omitempty" json:"default,omitempty"`
}

// Config holds default generation parameters for a prompt.
type Config struct {
	Temperature  *float64 `yaml:"temperature,omitempty" json:"temperature,omitempty"`
	MaxTokens    *int32   `yaml:"max_tokens,omitempty" json:"max_tokens,omitempty"`
	TopP         *float64 `yaml:"top_p,omitempty" json:"top_p,omitempty"`
	Stop         []string `yaml:"stop,omitempty" json:"stop,omitempty"`
	OutputSchema string   `yaml:"output_schema,omitempty" json:"output_schema,omitempty"`
}

// FileName returns the conventional file name for a slug.
func FileName(slug string) string { return slug + Ext }

// PathFor returns the conventional path of a prompt inside dir.
func PathFor(dir, slug string) string { return filepath.Join(dir, FileName(slug)) }

// Discover returns the sorted list of prompt files in dir. A missing directory
// yields an empty list rather than an error.
func Discover(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read %s: %w", dir, err)
	}

	var paths []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), Ext) {
			continue
		}
		paths = append(paths, filepath.Join(dir, e.Name()))
	}
	sort.Strings(paths)
	return paths, nil
}

// Parse validates YAML bytes against the embedded JSON Schema and decodes them.
func Parse(data []byte) (*PromptFile, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("prompt file is empty")
	}

	if err := Validate(data); err != nil {
		return nil, err
	}

	var pf PromptFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&pf); err != nil {
		return nil, fmt.Errorf("failed to decode prompt file: %w", err)
	}
	return &pf, nil
}

// Load reads and validates the prompt file at path.
func Load(path string) (*PromptFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pf, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return pf, nil
}

// Marshal renders a prompt file to YAML bytes, including the schema header.
func Marshal(pf *PromptFile) ([]byte, error) {
	if pf == nil {
		return nil, errors.New("nil prompt file")
	}

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "# yaml-language-server: $schema=%s\n", SchemaURL)

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(pf); err != nil {
		return nil, fmt.Errorf("failed to encode prompt file: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("failed to encode prompt file: %w", err)
	}
	return buf.Bytes(), nil
}

// Save validates a prompt file and writes it to path, creating parent
// directories as needed.
func Save(path string, pf *PromptFile) error {
	data, err := Marshal(pf)
	if err != nil {
		return err
	}
	if err := Validate(data); err != nil {
		return fmt.Errorf("refusing to write %s: %w", path, err)
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("failed to create %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

// FromProto converts a control-plane prompt into its file representation.
// Server-owned fields (id, version, status, audit timestamps) are intentionally
// dropped: the repository, not the database, owns the content.
func FromProto(p *promptv1.Prompt) *PromptFile {
	if p == nil {
		return nil
	}

	pf := &PromptFile{
		Name:        p.Name,
		Slug:        p.Slug,
		Description: p.Description,
	}
	if pf.Slug == "" {
		pf.Slug = Slugify(p.Name)
	}

	for _, m := range p.Messages {
		pf.Messages = append(pf.Messages, Message{Role: m.Role, Content: m.Content})
	}

	for _, v := range p.Variables {
		variable := Variable{
			Name:        v.Name,
			Type:        v.Type,
			Required:    v.Required,
			Description: v.Description,
		}
		if v.DefaultValue != "" {
			def := v.DefaultValue
			variable.Default = &def
		}
		pf.Variables = append(pf.Variables, variable)
	}

	if c := p.DefaultConfig; c != nil {
		cfg := &Config{Stop: c.Stop, OutputSchema: c.OutputSchema}
		if c.Temperature != 0 {
			t := c.Temperature
			cfg.Temperature = &t
		}
		if c.MaxTokens != 0 {
			m := c.MaxTokens
			cfg.MaxTokens = &m
		}
		if c.TopP != 0 {
			t := c.TopP
			cfg.TopP = &t
		}
		if !cfg.isZero() {
			pf.Config = cfg
		}
	}

	pf.Tags = p.Tags
	if len(p.Metadata) > 0 {
		pf.Metadata = make(map[string]string, len(p.Metadata))
		for k, v := range p.Metadata {
			pf.Metadata[k] = v
		}
	}

	return pf
}

func (c *Config) isZero() bool {
	return c == nil ||
		(c.Temperature == nil && c.MaxTokens == nil && c.TopP == nil &&
			len(c.Stop) == 0 && c.OutputSchema == "")
}

func (pf *PromptFile) protoMessages() []*promptv1.PromptMessage {
	msgs := make([]*promptv1.PromptMessage, len(pf.Messages))
	for i, m := range pf.Messages {
		msgs[i] = &promptv1.PromptMessage{Role: m.Role, Content: m.Content}
	}
	return msgs
}

func (pf *PromptFile) protoVariables() []*promptv1.PromptVariable {
	if len(pf.Variables) == 0 {
		return nil
	}
	vars := make([]*promptv1.PromptVariable, len(pf.Variables))
	for i, v := range pf.Variables {
		pv := &promptv1.PromptVariable{
			Name:        v.Name,
			Description: v.Description,
			Type:        v.Type,
			Required:    v.Required,
		}
		if v.Default != nil {
			pv.DefaultValue = *v.Default
		}
		vars[i] = pv
	}
	return vars
}

func (pf *PromptFile) protoConfig() *promptv1.GenerationConfig {
	if pf.Config.isZero() {
		return nil
	}
	c := &promptv1.GenerationConfig{
		Stop:         pf.Config.Stop,
		OutputSchema: pf.Config.OutputSchema,
	}
	if pf.Config.Temperature != nil {
		c.Temperature = *pf.Config.Temperature
	}
	if pf.Config.MaxTokens != nil {
		c.MaxTokens = *pf.Config.MaxTokens
	}
	if pf.Config.TopP != nil {
		c.TopP = *pf.Config.TopP
	}
	return c
}

// ToCreateRequest builds a CreatePrompt request from a prompt file.
func (pf *PromptFile) ToCreateRequest() *promptv1.CreatePromptRequest {
	return &promptv1.CreatePromptRequest{
		Name:          pf.Name,
		Slug:          pf.Slug,
		Description:   pf.Description,
		Messages:      pf.protoMessages(),
		Variables:     pf.protoVariables(),
		DefaultConfig: pf.protoConfig(),
		Tags:          pf.Tags,
		Metadata:      pf.Metadata,
	}
}

// ToUpdateRequest builds an UpdatePrompt request (which creates a new version)
// for the remote prompt identified by id.
func (pf *PromptFile) ToUpdateRequest(id, changeDescription string) *promptv1.UpdatePromptRequest {
	return &promptv1.UpdatePromptRequest{
		Id:                id,
		Description:       pf.Description,
		Messages:          pf.protoMessages(),
		Variables:         pf.protoVariables(),
		DefaultConfig:     pf.protoConfig(),
		Tags:              pf.Tags,
		Metadata:          pf.Metadata,
		ChangeDescription: changeDescription,
	}
}

// Slugify converts an arbitrary string into a schema-valid slug.
func Slugify(s string) string {
	var b strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
