// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only

// Package number preserves the reference's Python decimal argument grammar.
// Native Git remains responsible for Git configuration integer/boolean syntax.
package number

import (
	"errors"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// DecimalASCII handles Unicode decimal digits accepted by int()/float(). Other
// characters remain unchanged and are validated by the corresponding grammar.
func DecimalASCII(s string) string {
	return strings.Map(func(r rune) rune {
		if r > 127 && unicode.IsDigit(r) {
			for _, table := range unicode.Digit.R16 {
				if r >= rune(table.Lo) && r <= rune(table.Hi) && (r-rune(table.Lo))%rune(table.Stride) == 0 {
					return '0' + (r-rune(table.Lo))/rune(table.Stride)%10
				}
			}
			for _, table := range unicode.Digit.R32 {
				if r >= rune(table.Lo) && r <= rune(table.Hi) && (r-rune(table.Lo))%rune(table.Stride) == 0 {
					return '0' + (r-rune(table.Lo))/rune(table.Stride)%10
				}
			}
		}
		return r
	}, s)
}
func Integer(s string) (*big.Int, error) {
	s = strings.TrimSpace(DecimalASCII(s))
	if !regexp.MustCompile(`^[+-]?[0-9](?:_?[0-9])*$`).MatchString(s) {
		return nil, errors.New("invalid decimal integer")
	}
	s = strings.ReplaceAll(s, "_", "")
	if len(strings.TrimLeft(s, "+-")) > 4300 {
		return nil, errors.New("integer exceeds reference decimal conversion limit")
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil, errors.New("invalid decimal integer")
	}
	return n, nil
}
func Float(s string) (float64, error) {
	s = strings.TrimSpace(DecimalASCII(s))
	if !regexp.MustCompile(`(?i)^[+-]?(?:[0-9](?:_?[0-9])*(?:\.(?:[0-9](?:_?[0-9])*)?)?|\.[0-9](?:_?[0-9])*)(?:e[+-]?[0-9](?:_?[0-9])*)?$`).MatchString(s) {
		return 0, errors.New("invalid decimal number")
	}
	return strconv.ParseFloat(strings.ReplaceAll(s, "_", ""), 64)
}
