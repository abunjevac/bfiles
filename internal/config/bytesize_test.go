package config_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abunjevac/bfiles/internal/config"
)

func TestParseByteSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want config.ByteSize
	}{
		{"1048576", 1 << 20},
		{"512K", 512 << 10},
		{"512k", 512 << 10},
		{"1M", 1 << 20},
		{"1.5M", 3 << 19},
		{"2 G", 2 << 30},
		{"1MiB", 1 << 20},
		{"64KB", 64 << 10},
		{"0", 0},
	}

	for _, tc := range tests {
		got, err := config.ParseByteSize(tc.in)
		require.NoError(t, err, tc.in)
		require.Equal(t, tc.want, got, tc.in)
	}

	for _, bad := range []string{"", "M", "1X", "-1K", "abc"} {
		_, err := config.ParseByteSize(bad)
		require.Error(t, err, bad)
	}

	require.Equal(t, "1M", config.ByteSize(1<<20).String())
	require.Equal(t, "1536K", config.ByteSize(3<<19).String())
	require.Equal(t, "1000", config.ByteSize(1000).String())
}

func TestByteSizeInConfig(t *testing.T) {
	t.Parallel()

	cfg, err := config.Parse("[preview]\nmax_bytes = \"2M\"\n")
	require.NoError(t, err)
	require.Equal(t, config.ByteSize(2<<20), cfg.Preview.MaxBytes)

	cfg, err = config.Parse("[preview]\nmax_bytes = 4096\n")
	require.NoError(t, err)
	require.Equal(t, config.ByteSize(4096), cfg.Preview.MaxBytes)

	_, err = config.Parse("[preview]\nmax_bytes = \"lots\"\n")
	require.Error(t, err)
}

func TestParseByteSizeEdges(t *testing.T) {
	t.Parallel()

	for _, bad := range []string{"8388608T", "9223372036854775807K", "9223372036854775808", "1I", "1iB", "1.5", "0.0001K", "1e3", "1..5K"} {
		_, err := config.ParseByteSize(bad)
		require.Error(t, err, bad)
	}

	tests := map[string]config.ByteSize{
		"9223372036854775807": 9223372036854775807,
		"8388607T":            8388607 << 40,
		"2.0":                 2,
		"1.1K":                1126,
		"0.0K":                0,
	}

	for in, want := range tests {
		got, err := config.ParseByteSize(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got, in)
	}
}
