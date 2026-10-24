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

// ParseByteSize parses a size such as "1048576", "512K", "1.5 M" or "2GiB".
func ParseByteSize(s string) (ByteSize, error) {
	t := strings.ToUpper(strings.TrimSpace(s))
	t = strings.TrimSuffix(strings.TrimSuffix(t, "B"), "I")

	i := strings.IndexFunc(t, func(r rune) bool { return (r < '0' || r > '9') && r != '.' })

	num, unit := t, ""
	if i >= 0 {
		num, unit = strings.TrimSpace(t[:i]), strings.TrimSpace(t[i:])
	}

	mult, ok := byteUnits[unit]
	if !ok || num == "" {
		return 0, fmt.Errorf("invalid size %q (use e.g. 512K, 1M, 2G)", s)
	}

	v, err := strconv.ParseFloat(num, 64)
	if err != nil || v < 0 || v*float64(mult) > math.MaxInt64 {
		return 0, fmt.Errorf("invalid size %q (use e.g. 512K, 1M, 2G)", s)
	}

	return ByteSize(v * float64(mult)), nil
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
