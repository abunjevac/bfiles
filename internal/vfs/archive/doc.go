// Package archive exposes an archive file (zip, tar and compressed tar, 7z, rar) as a
// vfs.FS. Writable formats buffer changes in an overlay and rebuild the archive
// atomically on Commit.
package archive
