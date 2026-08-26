package ctags

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// binaryNames are tried in order. The Debian/Ubuntu package installs
// both; `ctags` may also be an Exuberant Ctags binary, which is a
// different, unmaintained program with no JSON output — hence the
// version check in detect rather than trusting either name.
var binaryNames = []string{"ctags", "ctags-universal"}

// universalMarker is the substring Universal Ctags prints in its version
// banner: "Universal Ctags 5.9.0, Copyright (C) 2015 Universal Ctags Team".
const universalMarker = "Universal Ctags"

// baseArgs are the flags every invocation uses.
//
// --kinds-C=+p and --kinds-C++=+p are load-bearing: the `prototype` kind
// ships DISABLED, and without it a header declaration produces no tag at
// all rather than a labelled one, which is indistinguishable from a call
// site. Do not remove them.
var baseArgs = []string{
	"--output-format=json",
	"--fields=+neKS",
	"--kinds-C=+p",
	"--kinds-C++=+p",
}

var (
	detectOnce sync.Once
	resolved   string // the binary to run, empty when unavailable
)

// detect finds a Universal Ctags binary once per process.
func detect() {
	for _, name := range binaryNames {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		out, err := exec.Command(path, "--version").Output()
		if err != nil {
			continue
		}
		if strings.Contains(string(out), universalMarker) {
			resolved = path
			return
		}
	}
}

// Available reports whether a Universal Ctags binary was found. Callers
// use it to decide between exact labelling and their own fallback.
func Available() bool {
	detectOnce.Do(detect)
	return resolved != ""
}

// TagsForFile runs ctags on an existing file and returns its tags. The
// file's extension determines the language, so callers writing content
// to a temp file must preserve the original suffix. ctags cannot read
// source from stdin.
//
// A file in a language ctags does not recognize yields no tags and no
// error, which callers treat the same as no matches.
func TagsForFile(ctx context.Context, path string) ([]Tag, error) {
	detectOnce.Do(detect)
	if resolved == "" {
		return nil, fmt.Errorf("universal-ctags not available")
	}

	// ctags cannot tell us "the file was not there": on a missing or
	// unreadable path it still exits 0 and prints only a stderr warning
	// -- exactly what it does for a file in a language it does not
	// recognize (see the doc comment above). So the exec error check
	// below can never catch a bad path; it would just come back as an
	// empty, error-free result indistinguishable from "ctags ran and
	// found nothing." Stat the path ourselves first:
	//   - it matches this function's own contract, "runs ctags on an
	//     existing file";
	//   - it keeps "no tags" meaning exactly one thing (ctags ran and
	//     found nothing), which matters because callers fall back to a
	//     heuristic when a file has no tags -- that fallback must not
	//     fire just because the path was missing;
	//   - it leaves the load-bearing --kinds flags above untouched.
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("ctags: cannot read %s: %w", path, err)
	}

	args := append(append([]string{}, baseArgs...), path)
	out, err := exec.CommandContext(ctx, resolved, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("ctags %s: %w", path, err)
	}
	return ParseJSONLines(out), nil
}
