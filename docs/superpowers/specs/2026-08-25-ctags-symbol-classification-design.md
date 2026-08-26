# ctags-backed symbol classification — design

Date: 2026-08-25

## Problem

The symbol references gutter labels each hit `definition`, `reference`, `import`,
`comment` or `string` using a line-local textual heuristic. On C++ the heuristic
over-reports definitions: a rule that accepted any preceding identifier before an
opening parenthesis fired on every declaration in every header. A tightening pass
(keyword adjacency plus Go receiver-method and C++ typed-declaration shapes) traded
that for a worse false-positive class and was reverted. Textual rules have plateaued.

## Goal

Label hits using a real per-language parser instead of regular expressions, and use
what that parser knows to qualify each hit: which kind of thing it is, and what
encloses it.

`git grep` remains the search. This changes only how hits are labelled.

## Decisions

| Question | Decision |
|---|---|
| Ceiling | Classification only. Not semantic reference resolution — no overload resolution, no cross-file linking. |
| Tool | universal-ctags, executed as a one-shot CLI. Not tree-sitter (needs CGO or an unproven pure-Go WASM host), not clangd (a far larger subsystem). |
| Depth | Surface the parser's kind, scope and signature in each row, not just a corrected definition/reference badge. |
| Missing tool | Fall back to the existing heuristic and say so in the gutter. Never break, never silently degrade. |

ctags is executed the way `git` already is throughout this codebase — `exec.CommandContext`
with a fixed argument list, one shot, read stdout. It is not a sidecar: there is no
process to supervise, no protocol, and no version handshake.

## Facts this design rests on

All verified on this machine against real redpanda files before the design was written.

- **Version.** Ubuntu 24.04 ships `universal-ctags` 5.9.20210829.0-1, reporting
  `Universal Ctags 5.9.0`. The package `Depends: libjansson4`, so JSON output is
  compiled in; `ctags --list-features` lists `json`.
- **Two binaries exist.** The package installs both `/usr/bin/ctags` and
  `/usr/bin/ctags-universal`. `exuberant-ctags` is a separate package in the same
  archive: a different, unmaintained program with no JSON output. Identity must be
  confirmed from `--version`, never assumed from the binary's name.
- **stdin is not supported.** `ctags -` fails with `Unknown option: -`. Content must
  reach ctags as a file.
- **The file extension drives language detection.** A temp file created with the
  original path's suffix is parsed as the right language with no `--language-force`
  and no extension-to-language table to maintain. Verified with `.h` (C++) and `.c` (C).
- **JSON output** is one object per line, carrying `_type`, `name`, `path`, `pattern`,
  `line`, `kind`, and — with `--fields=+neKS` — `scope`, `scopeKind`, `signature`,
  `typeref` and `end`.
- **`prototype` is disabled by default.** For C and C++, the `p` kind ships off, along
  with `local`, `externvar`, `alias`, `using`, `tparam`, `name`, `macroparam`, `label`
  and `parameter`. Without it a member declaration produces **no tag at all**, which is
  indistinguishable from a call site. `--kinds-C=+p --kinds-C++=+p` enables it; verified
  that `int m(int x);` inside a class then yields
  `{name: m, kind: prototype, scope: ns::c, signature: (int x)}`.
- **Real redpanda code parses cleanly.** `src/v/kafka/server/handlers/handler.h` and
  `src/v/kafka/server/app.cc` produced kinds `typedef`, `function`, `struct` and
  `namespace` with fully qualified scopes (`kafka::handler_template`,
  `kafka::server_app`). The template- and coroutine-heavy seastar constructs that were
  the expected stress case gave it no trouble.

## Architecture and data flow

After the endpoint partitions hits into in-PR and elsewhere, it collects the distinct
file paths among the retained hits — a handful per search, never the repository.

Content comes from the existing `Blob` path (`git cat-file -p <sha>:<path>`), not from
disk. Disk is not an option: in PR mode the clone is bare and has no working tree, and
in worktree mode disk holds the working state rather than the SHA that was searched.

Each file's content is written to a temp file preserving the original extension, then:

```
ctags --output-format=json --fields=+neKS --kinds-C=+p --kinds-C++=+p <tmpfile>
```

Tags are indexed by line. For each hit: if a tag sits on that line **and its name
matches the searched symbol**, the hit takes that tag's kind and scope. Otherwise the
existing line-local heuristic decides comment, string or import, falling through to
`reference`.

The work sits behind the current seam: `Classify` becomes a per-file labelling call
rather than a per-line one. The endpoint, the store and the gutter keep their shape.

## Kind taxonomy and API shape

`kind` keeps its five closed values. Sorting, file grouping and the comment/string
collapse all key off it, and that contract spans three layers.

ctags kinds map into those buckets: `function`, `func`, `method`, `class`, `struct`,
`member`, `macro`, `typedef`, `enum`, `union`, `namespace` and `prototype` all become
`definition`. ctags kind names are per-language rather than universal — C, C++ and
TypeScript emit `function` for a function definition, but Go emits `func` for both
functions and methods instead — which is why the map carries both spellings. Placing
`prototype` there is deliberate — a header declaration belongs beside the definition
when scanning; the problem was that declarations were indistinguishable from
definitions, not that they should be hidden.

Precision arrives as one optional object per hit rather than a scatter of flat fields:

```json
{ "path": "src/v/kafka/server/handlers/handler.cc", "line": 112,
  "text": "...", "kind": "definition",
  "tag": { "kind": "function", "scope": "kafka::handler_template",
           "signature": "(request)" } }
```

Only `kind`, `scope` and `signature` are surfaced; `scopeKind`, `typeref` and `end` are
available in the output but deliberately unused, to keep the row legible.

`tag` is present only when ctags matched that line, so "was this tagged?" is a single
null check and the display rule reads directly: show `tag.scope::symbol tag.signature`
badged with `tag.kind` when `tag` exists, else the raw text badged with `kind`.

The response also gains a top-level `classifier` field, `"ctags"` or `"heuristic"`, so
the degraded note is driven by an explicit fact rather than inferred from absent data.
That is what keeps the fallback a display difference instead of a second code path.

This changes the OpenAPI schema, so `make api-generate` must run and all five
regenerated artifacts must be committed.

## Gutter layout

A tagged hit shows its qualified name and signature, badged with the ctags kind. An
untagged hit keeps the raw matched source line and a `reference` badge.

```
- src/v/kafka/server/handlers/handler.h
   40  prototype   kafka::handler_template::handle(request)
- src/v/kafka/server/handlers/handler.cc
  112  function    kafka::handler_template::handle(request)
  118  reference     return handle(std::move(r));
- src/v/kafka/server/admin.cc
   77  function    kafka::admin_server::handle(request)
```

This makes the header declaration and the real definition visibly different, and makes
two same-named methods on different types distinguishable without reading paths.

The cost: tagged rows no longer show the literal source line, losing the return type
and qualifiers such as `final` or `noexcept`. The raw line moves to a hover tooltip,
recovering it without spending width in a narrow column.

When ctags is unavailable no hit carries a `tag`, so every row falls back to raw text
plus the coarse badge — today's exact appearance — with one line in the gutter header
noting that labels are heuristic and why.

Grouping, sort order and the comment/string collapse are untouched; `tag` is purely
display.

## Error handling and performance

Detection runs once per process and is cached: try `ctags`, then `ctags-universal`, and
confirm `--version` reports "Universal Ctags". An Exuberant binary on PATH is therefore
treated as absent rather than run and misparsed.

Failure isolation is per file. A file that fails to parse, that is in a language ctags
does not recognize, or that comes back truncated by the existing 2 MB blob cap, falls back to the heuristic for that file's hits only. A
pathological file never degrades or fails the whole search.

One temp file per distinct hit file, removed with `defer`. The ctags subprocess inherits
the request context, so a client disconnect cancels it, and it reuses the existing
10-second timeout rather than introducing another. Files are processed sequentially:
a handful at milliseconds each, where parallelism would be complexity without a problem.

No caching in this version. It should be added on evidence, not on a guessed hit rate.

One security property, stated deliberately because this branch already fixed a command
injection: **the searched symbol never reaches the ctags command line.** ctags is invoked
only with a generated temp path and literal flags; symbol matching happens in Go against
the parsed output. The SHA is already constrained by the existing hex guard. This adds a
third subprocess call and no new injection surface.

## Testing

Two pure functions carry most of the coverage and need no ctags installed: the JSON
parser (lines to tag records) and the join (hits plus tags plus symbol to labelled
hits). Both are table-tested, with fixtures captured from **real** ctags output on
redpanda files rather than invented JSON, so the parser is tested against what the tool
actually emits.

The join's cases that matter: a tag on the line whose name matches becomes tagged; a tag
on the line whose name is a different symbol does not, and falls through to the
heuristic; no tag falls through; and comment and string still win where they should.

One integration test executes ctags and skips cleanly when it is absent, since CI may
not have it. It asserts specifically that a declaration yields a `prototype` — the exact
regression that would otherwise be silent, because without the kind flag the result is
no tag rather than an error.

The existing heuristic tests remain as the fallback path's coverage.

Frontend tests cover `tag` rendering, the untagged fallback, the degraded note driven by
`classifier`, and the tooltip carrying the raw line.

One end-to-end trap to avoid: the existing symbol-refs Playwright spec runs against a
real server, and on a machine without ctags it would see `classifier: "heuristic"` and
pass while proving nothing about the ctags path. Existing assertions stay
classifier-agnostic (they concern rows, grouping and jumping); one ctags-specific
assertion is guarded explicitly on the `classifier` field, so it either exercises ctags
or visibly skips.

## Out of scope

- Semantic resolution: overloads, type-aware disambiguation, cross-file references.
  That needs clangd and a `compile_commands.json`, and is a different subsystem.
- tree-sitter, in-process or otherwise. Revisit only if ctags proves inadequate on real
  C++; the seam is a pure function over `(symbol, file content)` returning labels, so a
  parser-backed implementation can replace it without touching the endpoint, the store
  or the gutter.
- Caching parsed output between searches.
- Bundling or vendoring a ctags binary.
- Making ctags a hard requirement.
