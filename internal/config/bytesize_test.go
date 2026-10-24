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
