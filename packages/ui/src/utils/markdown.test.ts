import { describe, expect, it, vi } from "vitest";

// DOMPurify needs a DOM at import time and we run vitest with the
// default node environment. Stub it to a passthrough so we can
// exercise the marked-side tokenizer logic without pulling in
// jsdom just for these tests.
vi.mock("dompurify", () => ({
  default: { sanitize: (s: string) => s },
}));

const { renderMarkdown } = await import("./markdown.js");

const REPO = { owner: "acme", name: "widget", sha: "deadbeef" };

describe("renderMarkdown file ref linking", () => {
  it("links path:line to the github blob URL at the supplied SHA", () => {
    const html = renderMarkdown("see internal/server/huma_routes.go:2267 for details", REPO);
    expect(html).toContain(
      'href="https://github.com/acme/widget/blob/deadbeef/internal/server/huma_routes.go#L2267"',
    );
    expect(html).toContain(">internal/server/huma_routes.go:2267<");
  });

  it("links path:line-line as a multi-line range", () => {
    const html = renderMarkdown("look at foo/bar.ts:10-25 here", REPO);
    expect(html).toContain(
      "/blob/deadbeef/foo/bar.ts#L10-L25",
    );
  });

  it("accepts path:line:col (treats col as end line)", () => {
    const html = renderMarkdown("a/b.go:42:7", REPO);
    // Column-form is rendered as a range; either form is fine for
    // navigation, the lock-down here is "no crash, link emitted".
    expect(html).toContain("/blob/deadbeef/a/b.go#L42-L7");
  });

  it("does not link when sha is absent", () => {
    const html = renderMarkdown("internal/x.go:5", { owner: "acme", name: "widget" });
    expect(html).not.toContain("<a");
    expect(html).toContain("internal/x.go:5");
  });

  it("does not link when no repo context is provided", () => {
    const html = renderMarkdown("internal/x.go:5");
    expect(html).not.toContain("<a");
  });

  it("requires an extension and a line number to link", () => {
    // No extension → no link.
    expect(renderMarkdown("internal/server:5", REPO)).not.toContain("<a");
    // Extension but no line → no link.
    expect(renderMarkdown("internal/x.go in passing", REPO)).not.toContain("<a");
  });

  it("does not link bare filenames when no resolver is provided", () => {
    // Without a resolver we can't safely guess the directory, so
    // leave bare filenames as plain text rather than emit a 404.
    const html = renderMarkdown("see huma_routes.go:2267 for the handler", REPO);
    expect(html).not.toContain("<a");
    expect(html).toContain("huma_routes.go:2267");
  });

  it("links bare filenames when the resolver gives a unique path", () => {
    const html = renderMarkdown("see huma_routes.go:2267", {
      ...REPO,
      resolveBareFile: (n) =>
        n === "huma_routes.go" ? "internal/server/huma_routes.go" : null,
    });
    expect(html).toContain(
      "/blob/deadbeef/internal/server/huma_routes.go#L2267",
    );
  });

  it("does not link bare filenames the resolver flags as ambiguous", () => {
    const html = renderMarkdown("see types.go:5 for details", {
      ...REPO,
      resolveBareFile: (n) => (n === "types.go" ? null : null),
    });
    expect(html).not.toContain("<a");
  });

  it("renders bare filenames as plain text while resolution is pending", () => {
    // Resolver returns undefined → state is "asking, not yet known"
    // → render as plain text. (Once the resolver state updates the
    // caller will re-invoke renderMarkdown via the cacheBust token.)
    const html = renderMarkdown("see foo.go:1", {
      ...REPO,
      resolveBareFile: () => undefined,
    });
    expect(html).not.toContain("<a");
    expect(html).toContain("foo.go:1");
  });

  it("verifies multi-segment paths through the resolver too", () => {
    // The resolver acts as a path validator: a non-existent multi-
    // segment path returns null → render as plain text rather than
    // emit a known-bad 404 link.
    const html = renderMarkdown("see internal/server/missing.go:3", {
      ...REPO,
      resolveBareFile: () => null,
    });
    expect(html).not.toContain("<a");
  });

  it("does not match version-like or time-like colon strings", () => {
    expect(renderMarkdown("version 1.2.3:5 is broken", REPO)).not.toContain("<a");
    expect(renderMarkdown("at 09:30:45 the build started", REPO)).not.toContain("<a");
  });

  it("links a bare filename inside a code span when the resolver finds it", () => {
    // The most common Claude pattern: `huma_routes.go:2267` in
    // backticks, with the directory omitted. Resolution gives us
    // the unique path; the codespan renderer wraps the result.
    const html = renderMarkdown(
      "the bug is in `huma_routes.go:2267`",
      {
        ...REPO,
        resolveBareFile: (n) =>
          n === "huma_routes.go" ? "internal/server/huma_routes.go" : null,
      },
    );
    expect(html).toContain(
      "/blob/deadbeef/internal/server/huma_routes.go#L2267",
    );
    // Code styling preserved inside the link.
    expect(html).toContain("<code>huma_routes.go:2267</code>");
  });

  it("leaves non-file-ref code spans alone", () => {
    const html = renderMarkdown("run `git log` to see commits", REPO);
    expect(html).not.toContain("<a");
    expect(html).toContain("<code>git log</code>");
  });

  it("links file refs inside inline code spans (`backtick-wrapped`)", () => {
    // Claude habitually wraps paths in backticks. Without explicit
    // codespan handling, marked consumes the `...` as one token and
    // our inline tokenizer never sees the path. The renderer must
    // post-process codespans to rescue these.
    const html = renderMarkdown(
      "see `internal/server/huma_routes.go:42` for the handler",
      REPO,
    );
    expect(html).toContain(
      "/blob/deadbeef/internal/server/huma_routes.go#L42",
    );
  });

  it("does not link inside fenced code blocks", () => {
    const html = renderMarkdown(
      "```\ninternal/x.go:5\n```",
      REPO,
    );
    // The inner text is preserved but no anchor wraps it.
    expect(html).toContain("internal/x.go:5");
    expect(html).not.toContain('href="https://github.com');
  });

  it("does not double-link inside an existing markdown link", () => {
    const html = renderMarkdown(
      "[see this](https://example.com/a.go:5)",
      REPO,
    );
    // Outer link is preserved; the file-ref tokenizer must skip
    // tokens that are already inside a link label.
    expect(html).toContain('href="https://example.com/a.go:5"');
    // No nested anchor.
    const anchorCount = (html.match(/<a /g) ?? []).length;
    expect(anchorCount).toBe(1);
  });

  it("coexists with the issue/PR ref extension (#123)", () => {
    const html = renderMarkdown(
      "fixes #42, see internal/x.go:5",
      REPO,
    );
    expect(html).toContain("issues/42");
    expect(html).toContain("/blob/deadbeef/internal/x.go#L5");
  });

  it("strips a leading ./ from the linked path", () => {
    const html = renderMarkdown("./pkg/foo.go:1", REPO);
    expect(html).toContain("/blob/deadbeef/pkg/foo.go#L1");
  });
});
