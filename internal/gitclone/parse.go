package gitclone

import (
	"bytes"
	"strconv"
	"strings"

	godiff "github.com/sourcegraph/go-diff/diff"
)

// ParseRawZ parses the output of `git diff --raw -z` into file metadata.
// Returns files in output order (same order as patch output).
func ParseRawZ(data []byte) []DiffFile {
	// Split on NUL. The format is:
	//   :oldmode newmode oldhash newhash status\0path\0
	//   For renames/copies: :... R100\0oldpath\0newpath\0
	parts := bytes.Split(data, []byte{0})
	var files []DiffFile

	i := 0
	for i < len(parts) {
		part := string(parts[i])
		if !strings.HasPrefix(part, ":") {
			i++
			continue
		}

		// Parse the status letter from the end of the header.
		fields := strings.Fields(part)
		if len(fields) < 5 {
			i++
			continue
		}
		statusRaw := fields[4]
		status, isRenameOrCopy := rawStatusToString(statusRaw)

		i++ // move to path
		if i >= len(parts) {
			break
		}
		path := string(parts[i])

		var oldPath string
		if isRenameOrCopy {
			oldPath = path
			i++ // move to new path
			if i >= len(parts) {
				break
			}
			path = string(parts[i])
		}

		if oldPath == "" {
			oldPath = path
		}

		files = append(files, DiffFile{
			Path:    path,
			OldPath: oldPath,
			Status:  status,
		})
		i++
	}
	return files
}

// NumstatEntry holds the per-file line counts and binary flag from
// `git diff --numstat -z` output.
type NumstatEntry struct {
	Additions int
	Deletions int
	Binary    bool
}

// ParseNumstatZ parses the output of `git diff --numstat -z`, keyed by new
// path. Each record is normally "<added>\t<deleted>\t<path>\0". Binary files
// report "-" for both counts. Renames use the three-part form
// "<added>\t<deleted>\t\0<oldpath>\0<newpath>\0" -- an empty path field
// followed by the old and new paths as their own NUL-terminated parts.
func ParseNumstatZ(data []byte) map[string]NumstatEntry {
	result := make(map[string]NumstatEntry)
	parts := bytes.Split(data, []byte{0})

	i := 0
	for i < len(parts) {
		rec := parts[i]
		i++
		if len(rec) == 0 {
			continue
		}
		fields := bytes.SplitN(rec, []byte("\t"), 3)
		if len(fields) < 3 {
			continue
		}
		addStr, delStr, path := string(fields[0]), string(fields[1]), string(fields[2])

		if path == "" {
			// Rename/copy form: the next two NUL-delimited parts are the
			// old and new paths. Key by new path, like the normal form.
			if i+1 >= len(parts) {
				break
			}
			path = string(parts[i+1])
			i += 2
		}

		var entry NumstatEntry
		if addStr == "-" && delStr == "-" {
			entry.Binary = true
		} else {
			entry.Additions, _ = strconv.Atoi(addStr)
			entry.Deletions, _ = strconv.Atoi(delStr)
		}
		result[path] = entry
	}
	return result
}

func rawStatusToString(s string) (status string, isRenameOrCopy bool) {
	if len(s) == 0 {
		return "modified", false
	}
	switch s[0] {
	case 'A':
		return "added", false
	case 'D':
		return "deleted", false
	case 'M':
		return "modified", false
	case 'R':
		return "renamed", true
	case 'C':
		return "copied", true
	case 'T':
		return "modified", false // type change
	default:
		return "modified", false
	}
}

// devNullPath is what git prints in place of a missing side of a diff (a
// pure addition has no "a/" side, a pure deletion has no "b/" side).
const devNullPath = "/dev/null"

// resolveSectionSide strips git's "a/" or "b/" prefix from one side of a
// patch section's file header, treating "/dev/null" as absent. The
// sourcegraph/go-diff library already runs strconv.Unquote on these names
// while reading the "--- "/"+++ " header lines, so a C-quoted path (git
// quotes any path with non-ASCII or special bytes unless core.quotepath is
// disabled, e.g. `"a/caf\303\251.txt"`) arrives here already unquoted --
// git's octal-escape quoting is a subset of Go's interpreted-string-literal
// escapes, so strconv.Unquote decodes it correctly. That means we don't
// need to pass -c core.quotepath=false to the patch invocations or unquote
// again ourselves; we only need to strip the literal "a/"/"b/" prefix below.
func resolveSectionSide(name string) (path string, present bool) {
	if name == "" || name == devNullPath {
		return "", false
	}
	if len(name) > 2 && (name[:2] == "a/" || name[:2] == "b/") {
		name = name[2:]
	}
	return name, true
}

// sectionPath resolves the path a patch section should be matched against:
// the new-side path, or the old-side path when the new side is absent
// (deletions, where "+++" is "/dev/null"). Returns ok=false when neither
// side resolves to a usable path, signaling the caller to fall back to
// positional matching.
func sectionPath(fd *godiff.FileDiff) (path string, ok bool) {
	if p, present := resolveSectionSide(fd.NewName); present {
		return p, true
	}
	if p, present := resolveSectionSide(fd.OrigName); present {
		return p, true
	}
	return "", false
}

// ParsePatch parses unified diff patch output and merges it with
// pre-populated file metadata from ParseRawZ. Sections are matched to raw
// files by path (new-side path, or old-side path for deletions), not by
// position: git's patch pass can omit a section for a raw file the --raw
// pass still lists (e.g. a whitespace-only file under `git diff -w`), and
// matching by index would then misassign every later file's hunks. Only
// when a section's path cannot be resolved at all do we fall back to
// matching by position, on the assumption that such a section still lines
// up with its raw counterpart.
func ParsePatch(patch []byte, rawFiles []DiffFile) []DiffFile {
	fileDiffs, _ := godiff.ParseMultiFileDiff(patch)
	if len(fileDiffs) == 0 {
		return rawFiles
	}

	pathIndex := make(map[string]int, len(rawFiles))
	for i, f := range rawFiles {
		pathIndex[f.Path] = i
	}

	// Raw files with no matching section keep their raw metadata, but get
	// a non-nil Hunks so callers (and JSON serialization) see `[]` rather
	// than relying on a separate nil-to-empty pass.
	for i := range rawFiles {
		rawFiles[i].Hunks = []Hunk{}
	}

	for i, fd := range fileDiffs {
		idx := -1
		if p, ok := sectionPath(fd); ok {
			if j, found := pathIndex[p]; found {
				idx = j
			}
			// If the resolved path isn't found in rawFiles at all, this is
			// an anomaly (patch and raw passes disagreeing on paths); skip
			// rather than risk assigning the hunk to the wrong file.
		} else if i < len(rawFiles) {
			// Positional fallback only when the section's path can't be
			// resolved at all (e.g. an unparseable header). A positional
			// guess otherwise is exactly the bug this function fixes.
			idx = i
		}
		if idx == -1 {
			continue
		}

		// Detect binary from extended headers.
		for _, ext := range fd.Extended {
			if strings.HasPrefix(ext, "Binary files ") || strings.HasPrefix(ext, "GIT binary patch") {
				rawFiles[idx].IsBinary = true
				break
			}
		}

		// Convert hunks.
		for _, h := range fd.Hunks {
			hunk := Hunk{
				OldStart: int(h.OrigStartLine),
				OldCount: int(h.OrigLines),
				NewStart: int(h.NewStartLine),
				NewCount: int(h.NewLines),
				Section:  h.Section,
			}
			oldNum := int(h.OrigStartLine)
			newNum := int(h.NewStartLine)

			// The library handles "\ No newline at end of file" internally:
			// - Orig side: sets OrigNoNewlineAt to the byte offset after the line
			// - New side: removes the trailing \n from Body
			body := h.Body
			newSideNoNewline := len(body) > 0 && body[len(body)-1] != '\n'
			bodyLines := strings.Split(string(body), "\n")

			byteOffset := 0
			for j, line := range bodyLines {
				if j == len(bodyLines)-1 && line == "" {
					continue
				}

				lineByteEnd := byteOffset + len(line) + 1 // +1 for \n separator

				if len(line) == 0 {
					hunk.Lines = append(hunk.Lines, Line{
						Type: "context", Content: "", OldNum: oldNum, NewNum: newNum,
					})
					oldNum++
					newNum++
					byteOffset = lineByteEnd
					continue
				}

				isLastRealLine := j == len(bodyLines)-1 ||
					(j == len(bodyLines)-2 && bodyLines[len(bodyLines)-1] == "")

				switch line[0] {
				case ' ':
					// Context lines appear on both sides; check both no-newline signals.
					noNL := (newSideNoNewline && isLastRealLine) ||
						(h.OrigNoNewlineAt > 0 && int32(lineByteEnd) == h.OrigNoNewlineAt)
					hunk.Lines = append(hunk.Lines, Line{
						Type: "context", Content: line[1:], OldNum: oldNum, NewNum: newNum, NoNewline: noNL,
					})
					oldNum++
					newNum++
				case '+':
					noNL := newSideNoNewline && isLastRealLine
					hunk.Lines = append(hunk.Lines, Line{
						Type: "add", Content: line[1:], NewNum: newNum, NoNewline: noNL,
					})
					newNum++
					rawFiles[idx].Additions++
				case '-':
					noNL := h.OrigNoNewlineAt > 0 && int32(lineByteEnd) == h.OrigNoNewlineAt
					hunk.Lines = append(hunk.Lines, Line{
						Type: "delete", Content: line[1:], OldNum: oldNum, NoNewline: noNL,
					})
					oldNum++
					rawFiles[idx].Deletions++
				}
				byteOffset = lineByteEnd
			}
			rawFiles[idx].Hunks = append(rawFiles[idx].Hunks, hunk)
		}
	}

	return rawFiles
}
