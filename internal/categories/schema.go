// Package categories parses and validates the per-category dynamic field
// schema (plan §2.2, §7 P3).
package categories

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Field types.
const (
	TypeText   = "text"
	TypeNumber = "number"
	TypeSelect = "select"
	TypeColor  = "color"
)

// Field is one descriptor from categories.field_schema.
type Field struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Required bool     `json:"required"`
	Options  []string `json:"options,omitempty"`
}

// ParseSchema decodes a categories.field_schema JSON array.
func ParseSchema(raw string) ([]Field, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var fields []Field
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return nil, fmt.Errorf("category field_schema: %w", err)
	}
	for _, f := range fields {
		switch f.Type {
		case TypeText, TypeNumber, TypeSelect, TypeColor:
		default:
			return nil, fmt.Errorf("category field %q: unknown type %q", f.Key, f.Type)
		}
	}
	return fields, nil
}

// ValidationError lists the per-field problems in a rejected submission.
type ValidationError struct {
	Problems map[string]string
}

func (e *ValidationError) Error() string {
	keys := make([]string, 0, len(e.Problems))
	for k := range e.Problems {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("invalid attributes: ")
	for i, k := range keys {
		if i > 0 {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "%s: %s", k, e.Problems[k])
	}
	return b.String()
}

var colorRe = regexp.MustCompile(`^#?[0-9a-fA-F]{3,8}$`)

// Validate checks a submitted attribute map against a schema and returns the
// cleaned map to store. Unknown keys are rejected, never silently stored
// (plan §2.2). Empty values are dropped rather than persisted as "".
func Validate(schema []Field, attrs map[string]string) (map[string]string, error) {
	known := make(map[string]Field, len(schema))
	for _, f := range schema {
		known[f.Key] = f
	}

	problems := map[string]string{}
	out := map[string]string{}

	for k, v := range attrs {
		f, ok := known[k]
		if !ok {
			problems[k] = "not a field of this category"
			continue
		}
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		switch f.Type {
		case TypeNumber:
			if _, err := strconv.ParseFloat(v, 64); err != nil {
				problems[k] = "must be a number"
				continue
			}
		case TypeSelect:
			if !contains(f.Options, v) {
				problems[k] = "must be one of: " + strings.Join(f.Options, ", ")
				continue
			}
		case TypeColor:
			// Free-text colors like "forest green" are the common case for a
			// wishlist, so only reject a malformed hex code.
			if strings.HasPrefix(v, "#") && !colorRe.MatchString(v) {
				problems[k] = "not a valid hex color"
				continue
			}
		}
		if len(v) > 200 {
			problems[k] = "too long (max 200 characters)"
			continue
		}
		out[k] = v
	}

	for _, f := range schema {
		if f.Required && out[f.Key] == "" {
			problems[f.Key] = "required"
		}
	}

	if len(problems) > 0 {
		return nil, &ValidationError{Problems: problems}
	}
	return out, nil
}

// Marshal renders an attribute map for storage, always as a JSON object.
func Marshal(attrs map[string]string) (string, error) {
	if attrs == nil {
		attrs = map[string]string{}
	}
	b, err := json.Marshal(attrs)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Unmarshal reads a stored items.attributes value.
func Unmarshal(raw string) map[string]string {
	out := map[string]string{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
