package extract

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

var currencySymbols = map[string]string{
	"$": "USD", "US$": "USD", "€": "EUR", "£": "GBP", "¥": "JPY", "₹": "INR",
	"CA$": "CAD", "A$": "AUD",
}

var priceDigits = regexp.MustCompile(`-?[0-9][0-9.,\x{00A0} ]*`)

// ParsePriceCents turns a price string into integer cents plus any currency it
// could infer. Money is integer cents end to end (plan §2) — this is the only
// place a decimal string is interpreted.
func ParsePriceCents(s string) (*int64, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, ""
	}

	currency := ""
	for sym, code := range currencySymbols {
		if strings.Contains(s, sym) {
			currency = code
			break
		}
	}
	if m := regexp.MustCompile(`(?i)\b(USD|EUR|GBP|CAD|AUD|JPY|CHF|SEK|NOK|DKK|INR|MXN)\b`).FindString(s); m != "" {
		currency = strings.ToUpper(m)
	}

	match := priceDigits.FindString(s)
	if match == "" {
		return nil, currency
	}
	num := strings.Map(func(r rune) rune {
		if r == ' ' || r == ' ' {
			return -1
		}
		return r
	}, strings.TrimSpace(match))
	num = strings.TrimRight(num, ".,")

	lastDot := strings.LastIndex(num, ".")
	lastComma := strings.LastIndex(num, ",")
	switch {
	case lastDot >= 0 && lastComma >= 0:
		// Whichever separator comes last is the decimal point.
		if lastComma > lastDot {
			num = strings.ReplaceAll(num, ".", "")
			num = strings.Replace(num, ",", ".", 1)
		} else {
			num = strings.ReplaceAll(num, ",", "")
		}
	case lastComma >= 0:
		// "1,299" is thousands; "12,99" is European decimal.
		if len(num)-lastComma-1 == 2 {
			num = strings.Replace(num, ",", ".", 1)
		} else {
			num = strings.ReplaceAll(num, ",", "")
		}
	}

	f, err := strconv.ParseFloat(num, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, currency
	}
	cents := int64(math.Round(f * 100))
	return &cents, currency
}

// NormalizeCurrency uppercases a currency code, tolerating symbols.
func NormalizeCurrency(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if code, ok := currencySymbols[s]; ok {
		return code
	}
	if len(s) == 3 {
		return strings.ToUpper(s)
	}
	return ""
}
