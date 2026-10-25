package config

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ByteSize is a size in bytes written as a number or with a unit: "512K", "1.5M",
// "2G", "1 MiB". Units are binary (K = 1024).
//
//nolint:recvcheck // UnmarshalTOML needs a pointer; String and MarshalText must work on values
type ByteSize int64

// byteUnits maps unit suffixes (without the optional "iB"/"B") to multipliers.
//
//nolint:gochecknoglobals // static table
var byteUnits = map[string]int64{
	"":  1,
	"K": 1 << 10,
	"M": 1 << 20,
	"G": 1 << 30,
	"T": 1 << 40,
}

// ParseByteSize parses a size such as "1048576", "512K", "1.5 M" or "2GiB". A
// fractional value with a unit is rounded to the nearest byte; a fractional byte
// count without a unit, or a non-zero value that rounds to zero, is rejected.
func ParseByteSize(s string) (ByteSize, error) {
	invalid := fmt.Errorf("invalid size %q (use e.g. 512K, 1M, 2G)", s)

	num, mult, ok := splitSize(s)
	if !ok {
		return 0, invalid
	}

	if !strings.Contains(num, ".") {
		n, err := strconv.ParseInt(num, 10, 64)
		if err != nil || n > math.MaxInt64/mult {
			return 0, invalid
		}

		return ByteSize(n * mult), nil
	}

	n, ok := roundBytes(num, mult)
	if !ok {
		return 0, invalid
	}

	return n, nil
}

// roundBytes converts a fractional number of units to whole bytes.
func roundBytes(num string, mult int64) (ByteSize, bool) {
	v, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, false
	}

	// float64(MaxInt64) is 2^63, so >= is what keeps the conversion in range
	n := math.Round(v * float64(mult))
	if n >= math.MaxInt64 || (mult == 1 && n != v) || (n == 0 && v != 0) {
		return 0, false
	}

	return ByteSize(n), true
}

// splitSize separates the number from its unit multiplier. "i" is only valid as
// part of a unit ("KiB"), never on its own.
func splitSize(s string) (string, int64, bool) {
	t := strings.TrimSuffix(strings.ToUpper(strings.TrimSpace(s)), "B")
	binary := strings.HasSuffix(t, "I")
	t = strings.TrimSuffix(t, "I")

	i := strings.IndexFunc(t, func(r rune) bool { return (r < '0' || r > '9') && r != '.' })

	num, unit := t, ""
	if i >= 0 {
		num, unit = strings.TrimSpace(t[:i]), strings.TrimSpace(t[i:])
	}

	mult, ok := byteUnits[unit]
	if !ok || num == "" || (binary && unit == "") {
		return "", 0, false
	}

	return num, mult, true
}

// String renders the size with the largest unit that divides it exactly.
func (b ByteSize) String() string {
	for _, u := range []string{"T", "G", "M", "K"} {
		if m := byteUnits[u]; b != 0 && int64(b)%m == 0 {
			return strconv.FormatInt(int64(b)/m, 10) + u
		}
	}

	return strconv.FormatInt(int64(b), 10)
}

// UnmarshalTOML accepts both a plain integer and a string with a unit.
func (b *ByteSize) UnmarshalTOML(v any) error {
	switch t := v.(type) {
	case int64:
		if t < 0 {
			return errors.New("size must not be negative")
		}

		*b = ByteSize(t)

		return nil
	case string:
		parsed, err := ParseByteSize(t)
		if err != nil {
			return err
		}

		*b = parsed

		return nil
	}

	return fmt.Errorf("invalid size %v", v)
}

// MarshalText writes the size in its compact unit form.
func (b ByteSize) MarshalText() ([]byte, error) {
	return []byte(b.String()), nil
}
