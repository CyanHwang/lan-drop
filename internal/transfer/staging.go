package transfer

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Uploads are written to a staging file and only renamed onto the real name
// once every byte has arrived. Publishing early would mean a 5 GB ISO that
// stopped at 60% sits in the share looking exactly like a finished one — and
// the next device to download it gets a corrupt file with no warning.
const (
	partSuffix     = ".part"
	partMetaSuffix = ".part.meta"
)

func partPath(abs string) string     { return abs + partSuffix }
func partMetaPath(abs string) string { return abs + partMetaSuffix }

// isPartArtifact reports whether a listing entry is upload bookkeeping rather
// than user data. Hidden from the listing so a half-finished transfer does not
// masquerade as a file the user can open.
func isPartArtifact(name string) bool {
	return strings.HasSuffix(name, partSuffix) || strings.HasSuffix(name, partMetaSuffix)
}

// partMeta pins staged bytes to the exact file they came from. Without it, a
// second device uploading a different file with the same name would resume
// into the first one's bytes and produce something neither device ever had.
//
// Size plus modification time is enough to tell two files apart in practice,
// and costs nothing compared to hashing gigabytes on both ends.
type partMeta struct {
	Size  int64 `json:"size"`
	MTime int64 `json:"mtime"`
}

func readPartMeta(abs string) (partMeta, bool) {
	var m partMeta
	b, err := os.ReadFile(partMetaPath(abs))
	if err != nil {
		return m, false
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, false
	}
	return m, true
}

// writePartMeta is best effort: a missing sidecar only makes the next resume
// conservative (it restarts), never corrupt.
func writePartMeta(abs string, m partMeta) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(partMetaPath(abs), b, 0o644)
}

// removePart deletes the staging file and its sidecar. Used when the staged
// bytes are known to belong to a different file.
func removePart(abs string) {
	_ = os.Remove(partPath(abs))
	removePartMeta(abs)
}

// removePartMeta drops only the sidecar, which is what a successful rename
// needs: the staging file itself has become the real one by then.
func removePartMeta(abs string) {
	_ = os.Remove(partMetaPath(abs))
}

// partTTL is how long an abandoned staging file is kept before the sweeper
// reclaims it. Long enough that a transfer paused overnight still resumes,
// short enough that a forgotten 5 GB upload does not sit there forever.
const partTTL = 7 * 24 * time.Hour

// removeStaleParts walks one share and deletes staging artifacts untouched
// since cutoff. It is metadata-only work — no file is opened — and every error
// is ignored: a file that cannot be removed right now will be caught on the
// next pass.
func removeStaleParts(root string, cutoff time.Time) int {
	removed := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !isPartArtifact(d.Name()) {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil || info.ModTime().After(cutoff) {
			return nil
		}
		if os.Remove(p) == nil {
			removed++
		}
		return nil
	})
	return removed
}

// uploadLocks serialises writers per destination path. Two devices pushing the
// same name at once would otherwise interleave their chunks into a single file
// that is neither of the originals.
type uploadLocks struct {
	mu sync.Mutex
	m  map[string]struct{}
}

func (u *uploadLocks) acquire(key string) (release func(), ok bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.m == nil {
		u.m = map[string]struct{}{}
	}
	if _, busy := u.m[key]; busy {
		return nil, false
	}
	u.m[key] = struct{}{}
	return func() {
		u.mu.Lock()
		delete(u.m, key)
		u.mu.Unlock()
	}, true
}

// humanBytes formats a byte count for error messages. Kept local rather than
// pulled from a library: it is four lines and one less dependency.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTP"[exp])
}
