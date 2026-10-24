package local

import (
	"context"
	"os"
	"os/user"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/abunjevac/bfiles/internal/vfs"
)

var _ vfs.Detailer = (*FS)(nil)

// Details reports owner, group, inode and times of p itself (symlinks are not followed).
func (*FS) Details(_ context.Context, p string) (vfs.Details, error) {
	fi, err := os.Lstat(p)
	if err != nil {
		return vfs.Details{}, err
	}

	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return vfs.Details{}, nil
	}

	return vfs.Details{
		Owner:  userName(st.Uid),
		Group:  groupName(st.Gid),
		Inode:  st.Ino,
		Links:  st.Nlink,
		Access: time.Unix(st.Atim.Unix()),
		Change: time.Unix(st.Ctim.Unix()),
	}, nil
}

// name lookups go through NSS and can be slow, so results are cached
//
//nolint:gochecknoglobals // process-wide caches of id → name
var (
	namesMu sync.Mutex
	users   = map[uint32]string{}
	groups  = map[uint32]string{}
)

func userName(uid uint32) string {
	return cachedName(users, uid, func(id string) (string, error) {
		u, err := user.LookupId(id)
		if err != nil {
			return "", err
		}

		return u.Username, nil
	})
}

func groupName(gid uint32) string {
	return cachedName(groups, gid, func(id string) (string, error) {
		g, err := user.LookupGroupId(id)
		if err != nil {
			return "", err
		}

		return g.Name, nil
	})
}

func cachedName(cache map[uint32]string, id uint32, lookup func(string) (string, error)) string {
	namesMu.Lock()
	defer namesMu.Unlock()

	if n, ok := cache[id]; ok {
		return n
	}

	n, err := lookup(strconv.FormatUint(uint64(id), 10))
	if err != nil {
		n = strconv.FormatUint(uint64(id), 10)
	}

	cache[id] = n

	return n
}
