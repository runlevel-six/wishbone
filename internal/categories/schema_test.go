package categories_test

import (
	"errors"
	"testing"

	"wishbone/internal/categories"
)

const clothingSchema = `[
 {"key":"size","label":"Size","type":"text","required":false},
 {"key":"color","label":"Color","type":"text","required":false},
 {"key":"fit","label":"Fit","type":"select","required":false,"options":["mens","womens","unisex","kids"]},
 {"key":"count","label":"Count","type":"number","required":false}
]`

func schema(t *testing.T) []categories.Field {
	t.Helper()
	fields, err := categories.ParseSchema(clothingSchema)
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	return fields
}

// TestValidateRejectsUnknownKeys is the plan §8 requirement: unknown keys are
// rejected, not silently stored.
func TestValidateRejectsUnknownKeys(t *testing.T) {
	_, err := categories.Validate(schema(t), map[string]string{
		"size":         "M",
		"shoe_lace_id": "42",
	})
	var ve *categories.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("got %v, want a ValidationError", err)
	}
	if ve.Problems["shoe_lace_id"] == "" {
		t.Errorf("problems = %v, want one for the unknown key", ve.Problems)
	}
}

func TestValidateRejectsWrongTypes(t *testing.T) {
	cases := map[string]map[string]string{
		"number that is not a number": {"count": "a dozen"},
		"select outside its options":  {"fit": "toddler"},
	}
	for name, attrs := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := categories.Validate(schema(t), attrs); err == nil {
				t.Errorf("Validate(%v) should have failed", attrs)
			}
		})
	}
}

func TestValidateAcceptsGoodInput(t *testing.T) {
	clean, err := categories.Validate(schema(t), map[string]string{
		"size":  " M ",
		"fit":   "unisex",
		"count": "2",
		"color": "",
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if clean["size"] != "M" {
		t.Errorf("size = %q, want trimmed M", clean["size"])
	}
	if _, present := clean["color"]; present {
		t.Error("empty values should be dropped, not stored as empty strings")
	}
}

func TestRequiredFields(t *testing.T) {
	fields, err := categories.ParseSchema(`[{"key":"size","label":"Size","type":"text","required":true}]`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := categories.Validate(fields, map[string]string{}); err == nil {
		t.Error("a missing required field should fail validation")
	}
	if _, err := categories.Validate(fields, map[string]string{"size": "XL"}); err != nil {
		t.Errorf("a present required field should pass: %v", err)
	}
}

func TestParseSchemaRejectsUnknownType(t *testing.T) {
	if _, err := categories.ParseSchema(`[{"key":"x","label":"X","type":"wormhole"}]`); err == nil {
		t.Error("an unknown field type should be rejected at parse time")
	}
}

func TestRoundTrip(t *testing.T) {
	in := map[string]string{"size": "M", "fit": "unisex"}
	raw, err := categories.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	out := categories.Unmarshal(raw)
	if out["size"] != "M" || out["fit"] != "unisex" {
		t.Errorf("round trip lost data: %v", out)
	}
	if got := categories.Unmarshal(""); len(got) != 0 {
		t.Errorf("empty attributes should decode to an empty map, got %v", got)
	}
}
