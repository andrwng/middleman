import { describe, it, expect } from "vitest";
import { slugify, uniqueSlug } from "./headingSlug";

describe("slugify", () => {
  it("lowercases and hyphenates words", () => {
    expect(slugify("Data Flow")).toBe("data-flow");
  });

  it("drops punctuation but maps each space to a hyphen (GitHub-style, no collapsing)", () => {
    // "+" is dropped; each of its two surrounding spaces becomes a hyphen.
    expect(slugify("Record schemas + serde")).toBe("record-schemas--serde");
    expect(slugify("Feature activation + enable config + configs")).toBe(
      "feature-activation--enable-config--configs",
    );
  });

  it("keeps literal hyphens and underscores", () => {
    expect(slugify("Re-resolution triggers + metadata-hash feed")).toBe(
      "re-resolution-triggers--metadata-hash-feed",
    );
    expect(slugify("`consumer_group` type + dispatch")).toBe("consumer_group-type--dispatch");
  });

  it("strips code ticks, parens, dots, colons, and asterisks (matches the KIP-848 headings)", () => {
    expect(slugify("F2. Record schemas + serde (`ConsumerGroup*`)")).toBe(
      "f2-record-schemas--serde-consumergroup",
    );
    expect(slugify("C2. `consumer_group_stm`: persistence + recovery")).toBe(
      "c2-consumer_group_stm-persistence--recovery",
    );
    expect(slugify("R3. Assignor: `uniform` + SPI")).toBe("r3-assignor-uniform--spi");
  });
});

describe("uniqueSlug", () => {
  it("appends -1, -2 to repeated slugs", () => {
    const counts = new Map<string, number>();
    expect(uniqueSlug("Setup", counts)).toBe("setup");
    expect(uniqueSlug("Setup", counts)).toBe("setup-1");
    expect(uniqueSlug("Setup", counts)).toBe("setup-2");
  });

  it("falls back to 'section' for punctuation-only headings", () => {
    const counts = new Map<string, number>();
    expect(uniqueSlug("***", counts)).toBe("section");
  });
});
