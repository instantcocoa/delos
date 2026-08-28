package promptfile

import (
	"fmt"
	"sort"
	"strings"
)

// Change is one semantic difference between two prompt files.
type Change struct {
	Field  string // e.g. "messages[0]", "config.temperature", "name"
	Kind   string // "added" | "removed" | "changed" | "whitespace"
	Detail string // human-readable description
}

func (c Change) String() string {
	return fmt.Sprintf("%s %s: %s", c.Kind, c.Field, c.Detail)
}

// SemanticDiff compares two prompt files at the file-format level: message
// edits are classified (whitespace-only vs content), and every other field is
// compared for content, not merely for presence. An empty result means a push
// would be a no-op, so any field omitted here is a field whose edits are
// silently dropped - keep this in sync with PromptFile.
//
// Comparison order is deterministic (slice order for lists, sorted keys for
// maps) so callers can print the result directly.
//
// Config values are compared as the control plane would see them; see
// configView for the one documented blind spot that follows from proto3's
// inability to distinguish "unset" from "zero".
func SemanticDiff(a, b *PromptFile) []Change {
	var changes []Change

	scalar := func(field, av, bv string) {
		if av != bv {
			changes = append(changes, Change{
				Field: field, Kind: "changed",
				Detail: fmt.Sprintf("%q -> %q", truncate(av, 60), truncate(bv, 60)),
			})
		}
	}
	scalar("name", a.Name, b.Name)
	scalar("description", a.Description, b.Description)

	// messages: index-aligned comparison plus added/removed tails
	common := min(len(a.Messages), len(b.Messages))
	for i := 0; i < common; i++ {
		am, bm := a.Messages[i], b.Messages[i]
		field := fmt.Sprintf("messages[%d]", i)
		if am.Role != bm.Role {
			changes = append(changes, Change{Field: field, Kind: "changed",
				Detail: fmt.Sprintf("role %q -> %q", am.Role, bm.Role)})
			continue
		}
		if am.Content == bm.Content {
			continue
		}
		if squashSpace(am.Content) == squashSpace(bm.Content) {
			changes = append(changes, Change{Field: field, Kind: "whitespace",
				Detail: "whitespace-only change"})
		} else {
			changes = append(changes, Change{Field: field, Kind: "changed",
				Detail: fmt.Sprintf("content %q -> %q", truncate(am.Content, 40), truncate(bm.Content, 40))})
		}
	}
	for i := common; i < len(b.Messages); i++ {
		changes = append(changes, Change{Field: fmt.Sprintf("messages[%d]", i), Kind: "added",
			Detail: fmt.Sprintf("%s message %q", b.Messages[i].Role, truncate(b.Messages[i].Content, 40))})
	}
	for i := common; i < len(a.Messages); i++ {
		changes = append(changes, Change{Field: fmt.Sprintf("messages[%d]", i), Kind: "removed",
			Detail: fmt.Sprintf("%s message %q", a.Messages[i].Role, truncate(a.Messages[i].Content, 40))})
	}

	changes = append(changes, diffVariables(a.Variables, b.Variables)...)
	changes = append(changes, diffTags(a.Tags, b.Tags)...)
	changes = append(changes, diffMetadata(a.Metadata, b.Metadata)...)
	changes = append(changes, diffConfig(a.Config, b.Config)...)

	return changes
}

// diffVariables compares declared variables by name. Every attribute is
// compared, not just presence: a change to a variable's type, requiredness,
// description or default alters how the prompt renders and must be pushed.
//
// Variable order is not compared: rendering is by name, so reordering the list
// is not a semantic change.
func diffVariables(a, b []Variable) []Change {
	var changes []Change

	avars := make(map[string]Variable, len(a))
	for _, v := range a {
		avars[v.Name] = v
	}
	bvars := make(map[string]Variable, len(b))
	for _, v := range b {
		bvars[v.Name] = v
	}

	seen := make(map[string]bool, len(b))
	for _, bv := range b {
		if seen[bv.Name] {
			continue
		}
		seen[bv.Name] = true

		av, ok := avars[bv.Name]
		if !ok {
			changes = append(changes, Change{Field: "variables." + bv.Name, Kind: "added", Detail: "variable added"})
			continue
		}
		field := "variables." + bv.Name
		if av.Type != bv.Type {
			changes = append(changes, Change{Field: field + ".type", Kind: "changed",
				Detail: fmt.Sprintf("%s -> %s", orUnset(av.Type), orUnset(bv.Type))})
		}
		if av.Required != bv.Required {
			changes = append(changes, Change{Field: field + ".required", Kind: "changed",
				Detail: fmt.Sprintf("%t -> %t", av.Required, bv.Required)})
		}
		if av.Description != bv.Description {
			changes = append(changes, Change{Field: field + ".description", Kind: "changed",
				Detail: fmt.Sprintf("%q -> %q", truncate(av.Description, 60), truncate(bv.Description, 60))})
		}
		// PromptVariable.default_value is a plain proto3 string, so an absent
		// default and an empty one are the same value on the wire; comparing
		// the dereferenced strings keeps the diff from reporting a difference
		// that a push could never resolve.
		if deref(av.Default) != deref(bv.Default) {
			changes = append(changes, Change{Field: field + ".default", Kind: "changed",
				Detail: fmt.Sprintf("%q -> %q", truncate(deref(av.Default), 60), truncate(deref(bv.Default), 60))})
		}
	}

	seen = make(map[string]bool, len(a))
	for _, av := range a {
		if seen[av.Name] {
			continue
		}
		seen[av.Name] = true
		if _, ok := bvars[av.Name]; !ok {
			changes = append(changes, Change{Field: "variables." + av.Name, Kind: "removed", Detail: "variable removed"})
		}
	}
	return changes
}

// diffTags reports added and removed tags. Tags are a set, so a pure reorder is
// reported once as an order change rather than as a churn of add/remove pairs.
func diffTags(a, b []string) []Change {
	if equalStrings(a, b) {
		return nil
	}

	aset := make(map[string]bool, len(a))
	for _, t := range a {
		aset[t] = true
	}
	bset := make(map[string]bool, len(b))
	for _, t := range b {
		bset[t] = true
	}

	var changes []Change
	emitted := map[string]bool{}
	for _, t := range b {
		if !aset[t] && !emitted[t] {
			emitted[t] = true
			changes = append(changes, Change{Field: "tags", Kind: "added", Detail: fmt.Sprintf("tag %q", t)})
		}
	}
	emitted = map[string]bool{}
	for _, t := range a {
		if !bset[t] && !emitted[t] {
			emitted[t] = true
			changes = append(changes, Change{Field: "tags", Kind: "removed", Detail: fmt.Sprintf("tag %q", t)})
		}
	}
	if len(changes) == 0 {
		changes = append(changes, Change{Field: "tags", Kind: "changed",
			Detail: fmt.Sprintf("order changed: [%s] -> [%s]", strings.Join(a, ", "), strings.Join(b, ", "))})
	}
	return changes
}

// diffMetadata compares metadata entries key by key, in sorted key order.
func diffMetadata(a, b map[string]string) []Change {
	var changes []Change
	for _, k := range sortedKeys(b) {
		av, ok := a[k]
		switch {
		case !ok:
			changes = append(changes, Change{Field: "metadata." + k, Kind: "added",
				Detail: fmt.Sprintf("%q", truncate(b[k], 60))})
		case av != b[k]:
			changes = append(changes, Change{Field: "metadata." + k, Kind: "changed",
				Detail: fmt.Sprintf("%q -> %q", truncate(av, 60), truncate(b[k], 60))})
		}
	}
	for _, k := range sortedKeys(a) {
		if _, ok := b[k]; !ok {
			changes = append(changes, Change{Field: "metadata." + k, Kind: "removed",
				Detail: fmt.Sprintf("%q", truncate(a[k], 60))})
		}
	}
	return changes
}

// configView is a Config projected onto what the control plane can actually
// store. promptv1.GenerationConfig is proto3 with non-optional scalars, so
// temperature, max_tokens and top_p have no way to encode "unset" - an omitted
// field and a field explicitly set to zero arrive at the server as the same
// value. The diff therefore compares the values a push would transmit: a nil
// pointer and a zero value are equal, and an all-zero config is equal to no
// config at all.
//
// Documented limitation: a change that only flips a numeric config field
// between "unset" and 0 is not reported. That is deliberate - the server cannot
// observe such a change, so reporting it would produce a diff that no push can
// ever clear (the perpetual "config added or removed" that this replaced).
// Every value the wire format can carry is still compared exactly, including
// stop and output_schema, which are not zero-ambiguous.
type configView struct {
	present      bool
	temperature  float64
	maxTokens    int32
	topP         float64
	stop         []string
	outputSchema string
}

func viewConfig(c *Config) configView {
	var v configView
	if c == nil {
		return v
	}
	if c.Temperature != nil {
		v.temperature = *c.Temperature
	}
	if c.MaxTokens != nil {
		v.maxTokens = *c.MaxTokens
	}
	if c.TopP != nil {
		v.topP = *c.TopP
	}
	v.stop = c.Stop
	v.outputSchema = c.OutputSchema
	v.present = v.temperature != 0 || v.maxTokens != 0 || v.topP != 0 ||
		len(v.stop) > 0 || v.outputSchema != ""
	return v
}

func diffConfig(a, b *Config) []Change {
	av, bv := viewConfig(a), viewConfig(b)

	switch {
	case !av.present && !bv.present:
		return nil
	case av.present != bv.present:
		kind := "added"
		if !bv.present {
			kind = "removed"
		}
		return []Change{{Field: "config", Kind: kind, Detail: "generation config " + kind}}
	}

	var changes []Change
	if av.temperature != bv.temperature {
		changes = append(changes, Change{Field: "config.temperature", Kind: "changed",
			Detail: fmt.Sprintf("%s -> %s", floatStr(av.temperature), floatStr(bv.temperature))})
	}
	if av.maxTokens != bv.maxTokens {
		changes = append(changes, Change{Field: "config.max_tokens", Kind: "changed",
			Detail: fmt.Sprintf("%d -> %d", av.maxTokens, bv.maxTokens)})
	}
	if av.topP != bv.topP {
		changes = append(changes, Change{Field: "config.top_p", Kind: "changed",
			Detail: fmt.Sprintf("%s -> %s", floatStr(av.topP), floatStr(bv.topP))})
	}
	if !equalStrings(av.stop, bv.stop) {
		changes = append(changes, Change{Field: "config.stop", Kind: "changed",
			Detail: fmt.Sprintf("[%s] -> [%s]", strings.Join(av.stop, ", "), strings.Join(bv.stop, ", "))})
	}
	if av.outputSchema != bv.outputSchema {
		changes = append(changes, Change{Field: "config.output_schema", Kind: "changed",
			Detail: fmt.Sprintf("%q -> %q", truncate(av.outputSchema, 60), truncate(bv.outputSchema, 60))})
	}
	return changes
}

// Render substitutes {{variable}} placeholders using the declared variables:
// required variables must be provided, optional ones fall back to defaults.
//
// Substitution is single-pass: text produced by one substitution is never
// re-scanned, so a value that happens to contain "{{other}}" is emitted
// literally instead of being expanded (which previously depended on Go's
// randomized map iteration order and so was not even deterministic).
func Render(pf *PromptFile, vars map[string]string) ([]Message, error) {
	values := map[string]string{}
	for _, v := range pf.Variables {
		if v.Default != nil {
			values[v.Name] = *v.Default
		}
	}
	for k, v := range vars {
		values[k] = v
	}
	for _, v := range pf.Variables {
		if _, ok := values[v.Name]; v.Required && !ok {
			return nil, fmt.Errorf("required variable %q not provided (use --var %s=...)", v.Name, v.Name)
		}
	}

	out := make([]Message, len(pf.Messages))
	for i, m := range pf.Messages {
		out[i] = Message{Role: m.Role, Content: substitute(m.Content, values)}
	}
	return out, nil
}

// substitute replaces every "{{name}}" placeholder for which values has an
// entry, scanning the input exactly once. Unknown placeholders are left
// untouched.
func substitute(s string, values map[string]string) string {
	if !strings.Contains(s, "{{") {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	for {
		i := strings.Index(s, "{{")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		rest := s[i+2:]

		closeAt := strings.Index(rest, "}}")
		openAt := strings.Index(rest, "{{")
		if closeAt < 0 || (openAt >= 0 && openAt < closeAt) {
			// Not a well-formed placeholder starting here: emit the braces
			// literally and resume scanning after them.
			b.WriteString("{{")
			s = rest
			continue
		}

		name := rest[:closeAt]
		if v, ok := values[name]; ok {
			b.WriteString(v)
		} else {
			b.WriteString("{{")
			b.WriteString(name)
			b.WriteString("}}")
		}
		s = rest[closeAt+2:]
	}
}

func squashSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func floatStr(f float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.4f", f), "0"), ".")
}

func orUnset(s string) string {
	if s == "" {
		return "unset"
	}
	return s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sortedKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
