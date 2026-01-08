package testutil

import (
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed testdata/blns.json
var blnsJSON []byte

// NaughtyStrings provides access to the Big List of Naughty Strings (BLNS).
// https://github.com/minimaxir/big-list-of-naughty-strings
//
// These are strings that are known to cause issues in software systems.
// Use them for fuzzing, security testing, and input validation testing.
var NaughtyStrings = loadNaughtyStrings()

// NaughtyStringCategories provides categorized subsets of naughty strings
// for targeted testing.
var NaughtyStringCategories = categorizeStrings()

type naughtyStringSet struct {
	// All contains the complete BLNS list
	All []string

	// Categorized subsets for targeted testing
	Empty             []string
	Numeric           []string
	SpecialChars      []string
	Unicode           []string
	Zalgo             []string
	RTL               []string
	Emoji             []string
	Japanese          []string
	Arabic            []string
	ScriptInjection   []string
	SQLInjection      []string
	CommandInjection  []string
	PathTraversal     []string
	FormatStrings     []string
	ReservedFilenames []string
}

func loadNaughtyStrings() *naughtyStringSet {
	var all []string
	if err := json.Unmarshal(blnsJSON, &all); err != nil {
		// Fallback to minimal set if JSON fails to parse
		return &naughtyStringSet{
			All: []string{"", "null", "undefined", "'", "\"", "<script>", "../"},
		}
	}

	return &naughtyStringSet{All: all}
}

func categorizeStrings() *naughtyStringSet {
	base := loadNaughtyStrings()
	result := &naughtyStringSet{All: base.All}

	for _, s := range base.All {
		lower := strings.ToLower(s)

		// Empty/null values
		if s == "" || lower == "null" || lower == "nil" || lower == "none" || lower == "undefined" {
			result.Empty = append(result.Empty, s)
		}

		// Numeric edge cases
		if isNumericTest(s) {
			result.Numeric = append(result.Numeric, s)
		}

		// Special characters
		if isSpecialChars(s) {
			result.SpecialChars = append(result.SpecialChars, s)
		}

		// Unicode (non-ASCII)
		if hasNonASCII(s) && !isEmoji(s) && !isZalgo(s) {
			result.Unicode = append(result.Unicode, s)
		}

		// Zalgo text (combining characters)
		if isZalgo(s) {
			result.Zalgo = append(result.Zalgo, s)
		}

		// Right-to-left text
		if hasRTL(s) {
			result.RTL = append(result.RTL, s)
		}

		// Emoji
		if isEmoji(s) {
			result.Emoji = append(result.Emoji, s)
		}

		// Japanese text
		if hasJapanese(s) {
			result.Japanese = append(result.Japanese, s)
		}

		// Arabic text
		if hasArabic(s) {
			result.Arabic = append(result.Arabic, s)
		}

		// Script injection (XSS)
		if isScriptInjection(s) {
			result.ScriptInjection = append(result.ScriptInjection, s)
		}

		// SQL injection
		if isSQLInjection(s) {
			result.SQLInjection = append(result.SQLInjection, s)
		}

		// Command injection
		if isCommandInjection(s) {
			result.CommandInjection = append(result.CommandInjection, s)
		}

		// Path traversal
		if isPathTraversal(s) {
			result.PathTraversal = append(result.PathTraversal, s)
		}

		// Format strings
		if isFormatString(s) {
			result.FormatStrings = append(result.FormatStrings, s)
		}

		// Reserved filenames
		if isReservedFilename(s) {
			result.ReservedFilenames = append(result.ReservedFilenames, s)
		}
	}

	return result
}

func isNumericTest(s string) bool {
	numericPatterns := []string{"0", "1", "-1", "1.0", "-1.0", "NaN", "Infinity", "-Infinity", "1e", "0x", "0b", "0o"}
	for _, p := range numericPatterns {
		if strings.HasPrefix(s, p) || s == p {
			return true
		}
	}
	return false
}

func isSpecialChars(s string) bool {
	specials := []string{"\\", "/", "'", "\"", "`", "<", ">", "&", "|", ";", "$", "(", ")", "{", "}", "[", "]"}
	for _, sp := range specials {
		if s == sp || (len(s) <= 3 && strings.Contains(s, sp)) {
			return true
		}
	}
	return false
}

func hasNonASCII(s string) bool {
	for _, r := range s {
		if r > 127 {
			return true
		}
	}
	return false
}

func isZalgo(s string) bool {
	// Zalgo text has many combining characters (diacritics)
	combiningCount := 0
	for _, r := range s {
		// Combining Diacritical Marks: U+0300 - U+036F
		// Combining Diacritical Marks Extended: U+1AB0 - U+1AFF
		// Combining Diacritical Marks Supplement: U+1DC0 - U+1DFF
		if (r >= 0x0300 && r <= 0x036F) || (r >= 0x1AB0 && r <= 0x1AFF) || (r >= 0x1DC0 && r <= 0x1DFF) {
			combiningCount++
		}
	}
	return combiningCount > 5 // Zalgo typically has many combining chars
}

func hasRTL(s string) bool {
	rtlChars := []rune{
		0x200F, // Right-to-left mark
		0x202B, // Right-to-left embedding
		0x202E, // Right-to-left override
		0x2067, // Right-to-left isolate
	}
	for _, r := range s {
		for _, rtl := range rtlChars {
			if r == rtl {
				return true
			}
		}
		// Arabic range
		if r >= 0x0600 && r <= 0x06FF {
			return true
		}
		// Hebrew range
		if r >= 0x0590 && r <= 0x05FF {
			return true
		}
	}
	return false
}

func isEmoji(s string) bool {
	for _, r := range s {
		// Emoji ranges (simplified)
		if (r >= 0x1F300 && r <= 0x1F9FF) || // Misc Symbols, Emoticons, etc.
			(r >= 0x2600 && r <= 0x26FF) || // Misc Symbols
			(r >= 0x2700 && r <= 0x27BF) || // Dingbats
			(r >= 0x1F600 && r <= 0x1F64F) { // Emoticons
			return true
		}
	}
	return false
}

func hasJapanese(s string) bool {
	for _, r := range s {
		// Hiragana, Katakana, CJK
		if (r >= 0x3040 && r <= 0x309F) || // Hiragana
			(r >= 0x30A0 && r <= 0x30FF) || // Katakana
			(r >= 0x4E00 && r <= 0x9FFF) { // CJK Unified Ideographs
			return true
		}
	}
	return false
}

func hasArabic(s string) bool {
	for _, r := range s {
		if r >= 0x0600 && r <= 0x06FF {
			return true
		}
	}
	return false
}

func isScriptInjection(s string) bool {
	lower := strings.ToLower(s)
	patterns := []string{"<script", "javascript:", "onerror=", "onload=", "onclick=", "onfocus=", "onmouseover=", "eval(", "alert(", "document."}
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

func isSQLInjection(s string) bool {
	lower := strings.ToLower(s)
	patterns := []string{"' or ", "' and ", "union select", "drop table", "--", "/*", "*/", "1=1", "1'='1"}
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	// Classic patterns
	if strings.HasPrefix(s, "'") || strings.HasSuffix(s, "--") {
		return true
	}
	return false
}

func isCommandInjection(s string) bool {
	patterns := []string{"; ", "| ", "|| ", "&& ", "$(", "`", "\n", "/bin/", "/etc/", "cmd.exe", "powershell"}
	for _, p := range patterns {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

func isPathTraversal(s string) bool {
	patterns := []string{"../", "..\\", "%2e%2e", "....//", "/etc/passwd", "c:\\windows"}
	lower := strings.ToLower(s)
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

func isFormatString(s string) bool {
	patterns := []string{"%s", "%d", "%x", "%n", "%p", "{0}", "%(", "${"}
	for _, p := range patterns {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

func isReservedFilename(s string) bool {
	upper := strings.ToUpper(s)
	reserved := []string{"CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "LPT1", "LPT2", "LPT3"}
	for _, r := range reserved {
		if upper == r || strings.HasPrefix(upper, r+".") {
			return true
		}
	}
	return false
}

// RandomSample returns n random strings from the full list.
// Useful for quick fuzzing without testing the entire list.
func (n *naughtyStringSet) RandomSample(count int) []string {
	if count >= len(n.All) {
		return n.All
	}
	// Simple deterministic sampling for reproducibility
	result := make([]string, count)
	step := len(n.All) / count
	for i := 0; i < count; i++ {
		result[i] = n.All[i*step]
	}
	return result
}

// ForEach iterates through all strings, calling fn for each.
// Stops if fn returns false.
func (n *naughtyStringSet) ForEach(fn func(s string) bool) {
	for _, s := range n.All {
		if !fn(s) {
			return
		}
	}
}
