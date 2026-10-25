package sftpfs

// RenameReplace exposes the fallback used when the server lacks posix-rename.
func (f *FS) RenameReplace(from, to string) error { return f.renameReplace(from, to) }
