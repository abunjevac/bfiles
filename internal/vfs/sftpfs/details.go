package sftpfs

import (
	"context"
	"strconv"
	"time"

	"github.com/pkg/sftp"

	"github.com/abunjevac/bfiles/internal/vfs"
)

var _ vfs.Detailer = (*FS)(nil)

// Details reports numeric owner and group (SFTP does not carry names) and access time.
func (f *FS) Details(_ context.Context, p string) (vfs.Details, error) {
	fi, err := f.client.Lstat(p)
	if err != nil {
		return vfs.Details{}, err
	}

	st, ok := fi.Sys().(*sftp.FileStat)
	if !ok {
		return vfs.Details{}, nil
	}

	return vfs.Details{
		Owner:  strconv.FormatUint(uint64(st.UID), 10),
		Group:  strconv.FormatUint(uint64(st.GID), 10),
		Access: time.Unix(int64(st.Atime), 0),
	}, nil
}
