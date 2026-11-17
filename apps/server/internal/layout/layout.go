// Package layout names every path on the data volume, relative to its root.
//
//	<username>/<folders>/<name>                    current version of each visible file
//	.serversend/uploads/<upload>                   unfinished upload, chunks at their offsets
//	.serversend/versions/<account>/<file>/<version> every other retained version
//	.serversend/trash/<id>                         removal in progress
//	.serversend/tmp/                               unpublished batch files; cleared at startup
//	.serversend/untracked/<id>                     entries found in the way of a new file, kept
//
// Usernames are restricted to [a-z0-9._-] and never start with a dot, and
// names are validated logical names, so no component can escape its parent
// or collide with the internal area. The store also resolves every path
// through os.Root.
package layout

import (
	"path"

	"github.com/chironjit/serversend/shared/protocol"
)

const (
	Internal  = ".serversend"
	Uploads   = Internal + "/uploads"
	Versions  = Internal + "/versions"
	Trash     = Internal + "/trash"
	Tmp       = Internal + "/tmp"
	Untracked = Internal + "/untracked"
)

// Tree joins a user's folder names and a final name into a visible path.
func Tree(username string, names ...string) string {
	return path.Join(append([]string{username}, names...)...)
}

func Upload(id protocol.ID) string { return Uploads + "/" + string(id) }

func VersionsOf(account, file protocol.ID) string {
	return Versions + "/" + string(account) + "/" + string(file)
}

func Version(account, file, version protocol.ID) string {
	return VersionsOf(account, file) + "/" + string(version)
}

func TrashEntry(id protocol.ID) string { return Trash + "/" + string(id) }
