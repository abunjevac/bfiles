package icons_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abunjevac/bfiles/internal/icons"
)

func TestFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		isDir, isLink bool
		isExec        bool
		wantRole      icons.Role
	}{
		{"main.go", false, false, false, icons.RoleCode},
		{"backup.tar.gz", false, false, false, icons.RoleArchive},
		{"photo.JPG", false, false, false, icons.RoleImage},
		{"README.md", false, false, false, icons.RoleDoc},
		{".git", true, false, false, icons.RoleDir},
		{"run", false, false, true, icons.RoleExec},
		{"link", false, true, false, icons.RoleSymlink},
		{"main.go", false, true, false, icons.RoleSymlink},
		{"plain", false, false, false, icons.RoleFile},
	}

	for _, tc := range tests {
		g, role := icons.For(tc.name, tc.isDir, tc.isLink, tc.isExec, icons.StyleNerd)

		require.NotEmpty(t, g, tc.name)
		require.Equal(t, tc.wantRole, role, tc.name)
	}

	g, _ := icons.For(".git", true, false, false, icons.StyleNerd)
	plain, _ := icons.For("src", true, false, false, icons.StyleNerd)

	require.NotEqual(t, plain, g)

	g, role := icons.For("main.go", false, false, false, icons.StyleNone)
	require.Empty(t, g)
	require.Equal(t, icons.RoleCode, role)

	g, _ = icons.For("src", true, false, false, icons.StyleUnicode)
	require.Equal(t, "▸", g)
}

func TestIsArchive(t *testing.T) {
	t.Parallel()

	require.True(t, icons.IsArchive("a.tar.gz"))
	require.True(t, icons.IsArchive("A.ZIP"))
	require.True(t, icons.IsArchive("x.7z"))
	require.False(t, icons.IsArchive("a.gz"))
	require.False(t, icons.IsArchive("notes.txt"))
}
